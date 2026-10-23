import json
import os
import re
import shutil
import socket
import subprocess
import time
from pathlib import Path
from urllib.parse import urlsplit
from urllib.request import urlopen

import pytest
from playwright.sync_api import Page, expect, sync_playwright


ROOT = Path(__file__).resolve().parents[1]
FRONTEND = ROOT / "frontend"

MERGED_MENU = [
    "首页",
    "业务系统",
    "系统视图",
    "指标管理",
    "数据源",
    "采集管理",
    "数据管理",
    "告警中心",
    "通知订阅",
    "报表中心",
    "预测风险",
    "开放 API",
    "交易日",
    "系统管理",
]


def _free_port() -> int:
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        return int(sock.getsockname()[1])


@pytest.fixture(scope="session")
def app_url() -> str:
    npm = shutil.which("npm.cmd") or shutil.which("npm")
    if not npm:
        pytest.skip("npm is not available")

    port = _free_port()
    process = subprocess.Popen(
        [npm, "run", "dev", "--", "--host", "127.0.0.1", "--port", str(port), "--strictPort"],
        cwd=FRONTEND,
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
        env={**os.environ, "BROWSER": "none"},
    )
    url = f"http://127.0.0.1:{port}"
    try:
        deadline = time.time() + 30
        while time.time() < deadline:
            if process.poll() is not None:
                pytest.fail(f"Vite exited before startup with code {process.returncode}")
            try:
                with urlopen(url, timeout=1):
                    break
            except Exception:
                time.sleep(0.25)
        else:
            pytest.fail("Vite did not start within 30 seconds")
        yield url
    finally:
        if process.poll() is None:
            subprocess.run(
                ["taskkill", "/F", "/T", "/PID", str(process.pid)],
                stdout=subprocess.DEVNULL,
                stderr=subprocess.DEVNULL,
                check=False,
            )


@pytest.fixture(scope="session")
def browser():
    with sync_playwright() as playwright:
        instance = playwright.chromium.launch(headless=True)
        yield instance
        instance.close()


def _mock_api_data(method: str, path: str):
    if path == "/api/v1/auth/me":
        return {
            "id": 1,
            "username": "admin",
            "realName": "平台管理员",
            "dept": "平台组",
            "roles": ["platform_admin"],
            "mustChangePwd": False,
        }
    if method == "GET" and path == "/api/v1/scripts":
        return []
    if method == "GET" and path in {"/api/v1/data-sources", "/api/v1/data-source-categories"}:
        return []
    if method == "GET" and path == "/api/v1/alerts/stats":
        return {
            "byLevel": [],
            "activeByLevel": [],
            "topMetrics": [],
            "trend": [],
            "sla": [],
            "mttaMinutes": None,
            "mttrMinutes": None,
        }
    if method == "GET" and path == "/api/v1/alerts/dashboard-preference":
        return {
            "levels": ["p1", "p2", "p3", "p4"],
            "statuses": ["active", "processing", "recovered", "closed"],
            "alertTypes": ["threshold", "missing", "collect_fail", "anomaly", "ds_error"],
            "systemCodes": [],
            "dateMode": "today",
            "recentDays": 7,
            "sortRules": [{"field": "triggerAt", "direction": "desc"}],
        }
    if method == "PUT" and path == "/api/v1/alerts/dashboard-preference":
        return {
            "levels": ["p1", "p2", "p3", "p4"],
            "statuses": ["active", "processing", "recovered", "closed"],
            "alertTypes": ["threshold", "missing", "collect_fail", "anomaly", "ds_error"],
            "systemCodes": [],
            "dateMode": "today",
            "recentDays": 7,
            "sortRules": [{"field": "triggerAt", "direction": "desc"}],
        }
    if method == "GET" and path == "/api/v1/alerts/dashboard-options":
        return {"systems": [{"systemCode": "SYS_A", "name": "系统 A"}]}
    if method == "GET" and path == "/api/v1/alerts/dashboard":
        return {"items": [], "total": 0, "page": 1, "pageSize": 20}
    if method == "GET" and path == "/api/v1/alerts":
        return {"items": [], "total": 0, "page": 1, "pageSize": 20}
    if method == "GET" and path == "/api/v1/forecasts/risk":
        return {"run": None, "items": [], "count": 0}
    return {"items": [], "total": 0}


def _open_page(browser, app_url: str, path: str = "/dashboard") -> Page:
    context = browser.new_context(viewport={"width": 1440, "height": 1000})
    context.add_init_script("localStorage.setItem('cap_access_token', 'test-token')")

    def fulfill(route, request):
        path = urlsplit(request.url).path
        body = {
            "code": 0,
            "message": "ok",
            "data": _mock_api_data(request.method, path),
        }
        route.fulfill(status=200, content_type="application/json", body=json.dumps(body))

    context.route(re.compile(r"^https?://[^/]+/api/"), fulfill)
    page = context.new_page()
    page.goto(app_url + path)
    page.wait_for_selector(".aside .el-menu-item")
    return page


def test_sidebar_contains_only_merged_top_level_entries(browser, app_url):
    page = _open_page(browser, app_url)
    labels = [text.strip() for text in page.locator(".aside .el-menu-item").all_inner_texts()]

    assert labels == MERGED_MENU


def test_collection_management_contains_flow_script_and_task_tabs(browser, app_url):
    page = _open_page(browser, app_url, "/flows")
    tabs = page.locator(".module-tabs .el-tabs__item")

    assert [text.strip() for text in tabs.all_inner_texts()] == ["采集流程", "脚本管理", "任务监控"]

    tabs.filter(has_text="脚本管理").click()
    expect(page).to_have_url(re.compile(r"/flows\?tab=scripts$"))
    expect(tabs.filter(has_text="脚本管理")).to_have_class(re.compile(r"\bis-active\b"))
    expect(page.locator(".page-card .page-card")).to_have_count(0)

    tabs.filter(has_text="任务监控").click()
    expect(page).to_have_url(re.compile(r"/flows\?tab=tasks$"))
    expect(tabs.filter(has_text="任务监控")).to_have_class(re.compile(r"\bis-active\b"))


def test_forecast_risk_contains_event_tab(browser, app_url):
    page = _open_page(browser, app_url, "/forecast-risk")
    tabs = page.locator(".module-tabs .el-tabs__item")

    assert [text.strip() for text in tabs.all_inner_texts()] == ["风险清单", "事件因子"]

    tabs.filter(has_text="事件因子").click()
    expect(page).to_have_url(re.compile(r"/forecast-risk\?tab=events$"))
    expect(tabs.filter(has_text="事件因子")).to_have_class(re.compile(r"\bis-active\b"))
    expect(page.locator(".page-card .page-card")).to_have_count(0)


def test_alert_center_contains_custom_dashboard_tab(browser, app_url):
    page = _open_page(browser, app_url, "/alerts")
    tabs = page.locator(".module-tabs .el-tabs__item")

    assert [text.strip() for text in tabs.all_inner_texts()] == [
        "自定义看板",
        "告警列表",
        "告警统计",
        "告警策略",
    ]
    expect(tabs.filter(has_text="自定义看板")).to_have_class(re.compile(r"\bis-active\b"))
    expect(page).to_have_url(re.compile(r"/alerts$"))

    page.locator('[data-test="dashboard-save"]').click()
    expect(page.get_by_text("配置已保存")).to_be_visible()

    tabs.filter(has_text="告警策略").click()
    expect(page).to_have_url(re.compile(r"/alerts\?tab=policies$"))
    expect(tabs.filter(has_text="告警策略")).to_have_class(re.compile(r"\bis-active\b"))
    expect(page.locator(".page-card .page-card")).to_have_count(0)


@pytest.mark.parametrize(
    ("legacy_path", "expected_url"),
    [
        ("/scripts", r"/flows\?tab=scripts$"),
        ("/tasks", r"/flows\?tab=tasks$"),
        ("/forecast-events", r"/forecast-risk\?tab=events$"),
        ("/alert-policies", r"/alerts\?tab=policies$"),
    ],
)
def test_legacy_menu_urls_redirect_to_tabs(browser, app_url, legacy_path, expected_url):
    page = _open_page(browser, app_url, legacy_path)

    expect(page).to_have_url(re.compile(expected_url))
