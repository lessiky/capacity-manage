export function statusTag(s: string): string {
  return ({ active: 'danger', processing: 'primary', recovered: 'success', closed: 'info' } as Record<string, string>)[s] || 'info'
}

export function statusLabel(s: string): string {
  return ({ active: '活动', processing: '处理中', recovered: '已恢复', closed: '已关闭' } as Record<string, string>)[s] || s
}

export function typeLabel(t: string): string {
  return ({ threshold: '阈值', missing: '缺数', collect_fail: '采集失败', anomaly: '异常值', ds_error: '数据源异常' } as Record<string, string>)[t] || t
}

export function levelTag(level: string): string {
  return ({ p1: 'danger', p2: 'warning', p3: 'warning', p4: 'info' } as Record<string, string>)[level] || 'info'
}
