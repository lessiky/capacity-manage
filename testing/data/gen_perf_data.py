# -*- coding: utf-8 -*-
"""M5 性能测试造数 (WBS 5-2, 《测试计划》§9 "造数脚本参数化")

设计上限数据集: 5 万指标 × 200 业务日 = 1000 万行日表 (场景 5/7),
日增量 5 万行/日 × 30 天 (场景 4), 子集日内点 100 指标 × 30 天 × 10 点 (场景 3/4 写入)。
全部使用 Postgres INSERT..SELECT/generate_series 容器内生成 (无大文件)。
幂等: 重复执行先清理旧 PERF 数据。

运行: python testing/data/gen_perf_data.py [--keep | --teardown-only]
"""
import argparse
import subprocess
import sys
import time

CTR = "capacity-manage-postgres-1"
N_METRICS = 50000
N_SYSTEMS = 100
DAYS = 200
DATE_START = "2026-01-01"
DATE_END = "2026-07-19"          # 200 个自然日
RT_METRICS = 100                 # 日内点子集
RT_DAYS = 30
RT_POINTS = 10

def psql(sql, timeout=1800):
    t0 = time.time()
    r = subprocess.run(
        ["docker", "exec", CTR, "psql", "-U", "capacity", "-d", "capacity", "-v", "ON_ERROR_STOP=1", "-q", "-c", sql],
        capture_output=True, text=True, timeout=timeout)
    dt = time.time() - t0
    if r.returncode != 0:
        raise RuntimeError(f"psql fail ({dt:.1f}s): {r.stderr[:500]}")
    print(f"  psql ok ({dt:.1f}s): {sql.splitlines()[0][:90]}...")
    return dt

def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--keep", action="store_true", help="生成后保留")
    ap.add_argument("--teardown-only", action="store_true")
    args = ap.parse_args()

    if args.teardown_only:
        teardown()
        return
    t_all = time.time()
    print(f"== 造数: {N_METRICS} 指标 × {DAYS} 日 ({DATE_START}~{DATE_END}) ==")
    teardown()  # 幂等
    print("1) 分区")
    psql(f"SELECT ensure_partitions('metric_value_day', '{DATE_START}', '{DATE_END}')")
    psql(f"SELECT ensure_partitions('metric_value_rt', '{DATE_START}', '{DATE_END}')")
    print("2) 业务系统 x%d" % N_SYSTEMS)
    psql(f"""
        INSERT INTO business_system (system_code, name, owner, level, status, created_at, updated_at)
        SELECT 'PERF_SYS_' || lpad(((i-1)/500 + 1)::text, 3, '0'),
               '性能系统' || ((i-1)/500 + 1), 'perf', 'general', 'active', now(), now()
        FROM generate_series(1, {N_METRICS}) i
        WHERE i % 500 = 1
        ON CONFLICT (system_code) DO NOTHING""")
    print("3) 指标 x%d" % N_METRICS)
    dt = psql(f"""
        INSERT INTO capacity_metric (metric_code, name, system_code, category, metric_type, unit,
                                     ds_type, collect_method, baseline_source,
                                     stat_mode, judge_direction, threshold_mode, baseline, warn_line, danger_line,
                                     status, created_by, created_at, updated_at)
        SELECT 'PERF_M_' || lpad(i::text, 5, '0'),
               '性能指标' || i,
               'PERF_SYS_' || lpad(((i-1)/500 + 1)::text, 3, '0'),
               'storage', 'usage', '%', '', '', 'manual',
               'latest', 'higher_worse', 'percent',
               100.0, 70.0, 85.0,
               'active', 'perf', now(), now()
        FROM generate_series(1, {N_METRICS}) i
        ON CONFLICT (metric_code) DO NOTHING""")
    print(f"4) 日表 1000 万行 (INSERT..SELECT, 预计 1-3 分钟)")
    dt = psql(f"""
        INSERT INTO metric_value_day (metric_code, biz_date, day_max, day_min, day_avg, day_last,
                                      stat_value, water_level, level, point_count, source)
        SELECT 'PERF_M_' || lpad(m::text, 5, '0'),
               d::date,
               ROUND((100 + (m % 90) * 0.6 + (dnum % 10) * 0.05)::numeric, 2),
               ROUND((96 + (m % 90) * 0.6 + (dnum % 10) * 0.05 - 3)::numeric, 2),
               ROUND((98 + (m % 90) * 0.6 + (dnum % 10) * 0.05 - 1.5)::numeric, 2),
               ROUND((100 + (m % 90) * 0.6 + (dnum % 10) * 0.05 - 0.5)::numeric, 2),
               ROUND((100 + (m % 90) * 0.6 + (dnum % 10) * 0.05 - 0.5)::numeric, 2),
               ROUND(((m % 90) * 0.6 + (dnum % 10) * 0.05)::numeric / 100, 4),
               CASE WHEN (m % 90) >= 85 THEN 'danger'
                    WHEN (m % 90) >= 70 THEN 'warn'
                    ELSE 'normal' END,
               10, 'collect'
        FROM generate_series(1, {N_METRICS}) m,
             generate_series('{DATE_START}'::date, '{DATE_END}'::date, '1 day') d
        CROSS JOIN LATERAL (SELECT (d::date - '{DATE_START}'::date)::int AS dnum) L""", timeout=3600)
    print(f"   -> 1000 万行写入耗时 {dt:.1f}s")
    print("4.1) 预测质量门禁样例: 每个指标最近业务日质量分 1.0")
    psql(f"""
        INSERT INTO metric_quality_day (
            metric_code, biz_date, system_code, score, completeness, timeliness,
            anomaly_rate, duplicate_rate, freshness, valid_points, total_points, flags
        )
        SELECT 'PERF_M_' || lpad(i::text, 5, '0'),
               '{DATE_END}'::date,
               'PERF_SYS_' || lpad(((i-1)/500 + 1)::text, 3, '0'),
               1.0, 1.0, 1.0, 0.0, 0.0, 1.0, 1, 1, '[]'
        FROM generate_series(1, {N_METRICS}) i
        ON CONFLICT (metric_code, biz_date) DO UPDATE
        SET score = EXCLUDED.score, completeness = EXCLUDED.completeness,
            timeliness = EXCLUDED.timeliness, anomaly_rate = EXCLUDED.anomaly_rate,
            duplicate_rate = EXCLUDED.duplicate_rate, freshness = EXCLUDED.freshness,
            valid_points = EXCLUDED.valid_points, total_points = EXCLUDED.total_points,
            flags = EXCLUDED.flags, evaluated_at = now()""")
    print(f"5) 日内点: {RT_METRICS} 指标 × {RT_DAYS} 天 × {RT_POINTS} 点")
    dt = psql(f"""
        INSERT INTO metric_value_rt (metric_code, ts, raw_value, clean_value, is_anomaly, judge_level, source)
        SELECT 'PERF_M_' || lpad(m::text, 5, '0'),
               date_trunc('day', d::timestamp) + make_interval(hours => 1, mins => (p * 144) / {RT_POINTS}),
               ROUND((80 + (m % 90) * 0.6)::numeric, 2),
               ROUND((80 + (m % 90) * 0.6)::numeric, 2),
               false,
               CASE WHEN (m % 90) >= 85 THEN 'danger'
                    WHEN (m % 90) >= 70 THEN 'warn'
                    ELSE 'normal' END,
               'collect'
        FROM generate_series(1, {RT_METRICS}) m,
             generate_series(('{DATE_END}'::date - {RT_DAYS - 1}), '{DATE_END}'::date, '1 day') d,
             generate_series(1, {RT_POINTS}) p
        ON CONFLICT (metric_code, ts) DO NOTHING""", timeout=1800)
    print(f"   -> 日内点写入耗时 {dt:.1f}s")
    n = psql_count("SELECT count(*) FROM metric_value_day WHERE metric_code LIKE 'PERF_M_%'")
    print(f"校验: 日表 PERF 行数 = {n}")
    assert n == N_METRICS * DAYS, f"行数不符: {n}"
    total = time.time() - t_all
    print(f"== 造数完成, 总耗时 {total:.0f}s ==")

def psql_count(sql):
    r = subprocess.run(
        ["docker", "exec", CTR, "psql", "-U", "capacity", "-d", "capacity", "-t", "-A", "-c", sql],
        capture_output=True, text=True, timeout=600)
    if r.returncode != 0:
        raise RuntimeError(r.stderr[:300])
    return int(r.stdout.strip())

def teardown():
    print("清理旧 PERF 数据 ...")
    t0 = time.time()
    psql("DELETE FROM metric_quality_day WHERE metric_code LIKE 'PERF_M_%'", timeout=1800)
    psql("DELETE FROM forecast_ai_adjustment WHERE result_id IN "
         "(SELECT id FROM forecast_result WHERE metric_code LIKE 'PERF_M_%')", timeout=1800)
    psql("DELETE FROM forecast_result WHERE metric_code LIKE 'PERF_M_%'", timeout=1800)
    psql("DELETE FROM metric_value_rt WHERE metric_code LIKE 'PERF_M_%'", timeout=1800)
    psql("DELETE FROM metric_value_day WHERE metric_code LIKE 'PERF_M_%'", timeout=3600)
    psql("DELETE FROM capacity_metric WHERE metric_code LIKE 'PERF_M_%'")
    psql("DELETE FROM business_system WHERE system_code LIKE 'PERF_SYS_%'")
    print(f"清理完成 ({time.time() - t0:.0f}s)")

if __name__ == "__main__":
    main()
