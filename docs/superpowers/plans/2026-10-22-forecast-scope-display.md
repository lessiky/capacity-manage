# Forecast Scope, Risk Window and Display Toggles Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let administrators recompute only selected systems/metrics, make the risk window use the appropriate 30/90-day prediction, expose AI rationale/suggestions, and let the metric detail page hide the forecast card and overlay.

**Architecture:** Extend the existing forecast service with a scoped-run filter while preserving the full daily batch. Make the risk endpoint choose a display row per metric based on the selected window, considering both 30/90-day rows so larger windows remain supersets. Keep the frontend changes inside `ForecastRisk.vue` and `MetricDetail.vue`; no new backend routes are needed.

**Tech Stack:** Go 1.x + Gin + GORM (SQLite tests, PostgreSQL production), Vue 3 + TypeScript + Element Plus + ECharts.

**Spec:** This plan implements the agreed points from the 2026-10-22 session. The repository has no initial commit, so no per-task commits are possible; verification replaces the commit step.

## Global Constraints

- Keep the existing full-batch `POST /api/v1/forecasts/recompute?date=YYYY-MM-DD` behaviour working for `e2e_m7.py`.
- Preserve the admin permission `admin:health:view` on recompute and `metric:view` on risk reads.
- Risk rows must stay data-scope filtered through `capacity_metric.system_code`.
- Do not change the forecast engine's 30/90-day horizons.
- Keep frontend text Chinese, ASCII-only source files, no new dependencies.
- Backend tests must run with `go test ./backend/internal/forecast/...`.
- Frontend verification must run `npm run lint` and `npm run build` in `frontend/`.

---

### Task 1: Scoped forecast recompute

**Files:**
- Modify: `backend/internal/forecast/service.go`
- Modify: `backend/internal/forecast/handler.go`
- Test: `backend/internal/forecast/service_test.go`
- Test: `backend/internal/forecast/handler_test.go`

**Interfaces:**

```go
type RunFilter struct {
    SystemCodes []string
    MetricCodes []string
}

func (f RunFilter) Empty() bool

type RunSummary struct {
    Run                 *persistence.ForecastRun
    ScopeMetricsTotal   int
    ScopeMetricsDone    int
    ScopeMetricsSkipped int
}

func (s *Service) RunScopedResult(ctx context.Context, runDate time.Time, filter RunFilter) (*RunSummary, error)
```

- [x] **Step 1: Write failing service tests**

Add tests that:
1. Seed two metrics, run a full daily batch, then run a scoped batch for one metric.
2. Assert only the selected metric's `forecast_result` rows were replaced.
3. Assert the other metric's result row keeps its original `ID` and `InputDigest`.
4. Assert a partial filter with no matching active metric returns `ErrNoMatchedMetrics`.
5. Assert `SystemCodes` filters by `capacity_metric.system_code`.

Run: `go test ./backend/internal/forecast/... -run 'TestRunScoped' -count=1`
Expected: FAIL because `RunScopedResult`, `RunFilter` and `ErrNoMatchedMetrics` do not exist.

- [x] **Step 2: Implement the scoped run**

In `service.go`:
- Add `RunFilter`, `RunSummary`, `ErrNoMatchedMetrics`.
- Load metrics before creating/updating the run, applying `system_code IN ?` and `metric_code IN ?` when non-empty.
- Return `ErrNoMatchedMetrics` for a non-empty filter with zero metrics.
- Add `beginRunScoped(ctx, runDate, now, metricCodes, full)` which deletes all results for a full run and only `metric_code IN ?` for a scoped run.
- Add `refreshRunCounters(ctx, run)` using `COUNT(DISTINCT metric_code)` over `forecast_result` for total/done/skipped, where done uses `status = 'success'` and skipped uses `status <> 'success'`.
- Stop incrementing counters inside `saveBatch`; refresh once after all batches.
- Keep `RunDailyResult` returning `*persistence.ForecastRun` by delegating to `RunScopedResult(ctx, runDate, RunFilter{})`.

Run: `go test ./backend/internal/forecast/... -run 'TestRunScoped' -count=1`
Expected: PASS.

- [x] **Step 3: Write a failing handler test**

Add `TestRecomputeEndpointAcceptsSystemScope` that:
1. Seeds two systems/metrics with enough history.
2. Registers `POST /forecasts/recompute` on a test engine.
3. Posts `{"systemCodes":["SYS_M7_A"]}`.
4. Asserts the response is `scoped=true`, `scopeMetricsTotal=1`, and only the selected metric has `forecast_result` rows.
5. Asserts the old query-only path still works by posting `?date=YYYY-MM-DD` with no body.

Run: `go test ./backend/internal/forecast/... -run 'TestRecomputeEndpointAcceptsSystemScope' -count=1`
Expected: FAIL because the handler ignores the body.

- [x] **Step 4: Implement request parsing and response**

In `handler.go`:
- Parse an optional JSON body `{date, systemCodes, metricCodes}`; ignore `io.EOF` so bodyless calls still work.
- Fall back to `c.Query("date")`.
- Normalize codes by trimming, dropping empty strings and deduplicating.
- Call `h.svc.RunScopedResult`.
- Return a flat view embedding `persistence.ForecastRun` plus `scoped`, `scopeMetricsTotal`, `scopeMetricsDone`, `scopeMetricsSkipped`.
- Map `ErrNoMatchedMetrics` to `resp.CodeBadRequest`.

Run: `go test ./backend/internal/forecast/... -count=1`
Expected: PASS.

---

### Task 2: Risk window uses the right horizon

**Files:**
- Modify: `backend/internal/forecast/handler.go`
- Test: `backend/internal/forecast/handler_test.go`

**Interfaces:**

```go
func riskHorizonForWindow(days int) int // 30 for days <= 30, otherwise 90
func selectRiskRow(rows map[int]persistence.ForecastResult, preferred int, until time.Time, days int) (persistence.ForecastResult, bool)
```

- [x] **Step 1: Write failing risk tests**

Add a test with one metric carrying both horizons:
- 30-day row: ETA warn `+10d`, risk `0.90`.
- 90-day row: ETA warn `+70d`, risk `0.90`.

Assertions:
- `GET /forecasts/risk?days=30` returns `horizonDays=30` and ETA `+10d`.
- `GET /forecasts/risk?days=60` returns `horizonDays=90` and ETA `+70d`.
- `GET /forecasts/risk?days=90` returns `horizonDays=90` and ETA `+70d`.
- A second metric with only a 30-day row still appears for `days=90` with `horizonDays=30`.

Run: `go test ./backend/internal/forecast/... -run 'TestRiskEndpointUsesWindowHorizon' -count=1`
Expected: FAIL because the handler still hard-codes `horizon_days = 30`.

- [x] **Step 2: Implement the horizon selection**

In `Risk`:
- Query `horizon_days IN (30, 90)` with the existing status filter.
- Group rows by `metric_code` and `horizon_days`.
- Pick the preferred row (`30` for `days <= 30`, else `90`) when it satisfies `riskWithin`.
- If the preferred row does not satisfy the window, fall back to the other horizon.
- Keep the existing risk-score sorting.
- Batch-load metric metadata by selected `metric_code` instead of one query per row.

Run: `go test ./backend/internal/forecast/... -run 'TestRiskEndpoint' -count=1`
Expected: PASS.

---

### Task 3: Expose AI rationale and suggested ETA

**Files:**
- Modify: `backend/internal/forecast/handler.go`
- Test: `backend/internal/forecast/handler_test.go`
- Modify: `frontend/src/views/ForecastRisk.vue`
- Modify: `frontend/src/views/MetricDetail.vue`

**Interfaces:**

```go
func attachAIAdjustments(db *gorm.DB, views map[int64]gin.H)
func applyAIAdjustmentView(view gin.H, adj persistence.ForecastAIAdjustment)
```

- [x] **Step 1: Write a failing backend test**

Add `TestRiskEndpointExposesAIAdjustment`:
- Seed a run, a 30-day result, and a `ForecastAIAdjustment` with `status=oversized`.
- Put `suggestedEtaWarnDate` and `suggestedEtaDangerDate` in `AdjustedSnapshot`.
- Assert the risk item contains `aiStatus`, `aiRationale`, `adjustmentNote`, `suggestedEtaWarnDate`, `suggestedEtaDangerDate`.

Run: `go test ./backend/internal/forecast/... -run 'TestRiskEndpointExposesAIAdjustment' -count=1`
Expected: FAIL because `Risk` never attaches AI adjustments.

- [x] **Step 2: Implement batch AI attachment**

- Refactor `attachAIAdjustment` to call `applyAIAdjustmentView`.
- Parse `AdjustedSnapshot` for `adjustmentNote`, `suggestedEtaWarnDate`, `suggestedEtaDangerDate`.
- In `Risk`, attach AI adjustments for all displayed result IDs with one `result_id IN ?` query.

Run: `go test ./backend/internal/forecast/... -count=1`
Expected: PASS.

- [x] **Step 3: Show AI details in the risk list**

In `ForecastRisk.vue`:
- Add AI fields to the `Row` interface.
- Add an expandable row showing AI status, model, ETA shift, rationale/error, and suggested ETA dates.
- Add an `AI` column with a status tag (`AI 修正`, `AI 建议`, `AI 降级`, `-`).
- Include the AI status, model, rationale and suggested dates in CSV export.
- Update the subtitle to explain the risk-window rule.

Run: `cd frontend; npm run lint`
Expected: PASS.

- [x] **Step 4: Show AI rationale and suggested ETA in metric detail**

In `MetricDetail.vue`:
- Add `suggestedEtaWarnDate`, `suggestedEtaDangerDate`, `adjustmentNote` to `ForecastData`.
- Show two extra forecast rows when `aiStatus === 'oversized'` and suggested dates are present.
- Change `aiNote` so a successful adjustment shows `AI 修正理由：...`, oversized shows `AI 建议：...`, and degraded states keep the fallback/error text.

Run: `cd frontend; npm run lint`
Expected: PASS.

---

### Task 4: Selective recompute UI

**Files:**
- Modify: `frontend/src/views/ForecastRisk.vue`

- [x] **Step 1: Add the recompute dialog**

In `ForecastRisk.vue`:
- Replace the direct `recompute()` POST with an `el-dialog`.
- Fields: business date, range mode (`全部指标` / `部分指标或系统`), system multi-select, metric multi-select.
- Lazily load `/api/v1/systems?page=1&pageSize=200` and all `/api/v1/metrics` pages when the dialog opens.
- Filter metric options by selected systems.
- Require at least one system or metric in partial mode.
- POST `{date, systemCodes, metricCodes}` to `/api/v1/forecasts/recompute`.
- Show `scopeMetricsDone` / `scopeMetricsSkipped` when present, otherwise the run totals.

Run: `cd frontend; npm run lint`
Expected: PASS.

---

### Task 5: Metric detail display toggles

**Files:**
- Modify: `frontend/src/views/MetricDetail.vue`

- [x] **Step 1: Add persisted toggles**

- Add `showForecastCard` and `showForecastLine` refs backed by `localStorage` keys `cap_metric_show_forecast_card` and `cap_metric_show_forecast_line`, default `true`.
- Add a `容量预测` switch to the top toolbar and a `预测线` switch to the trend chart toolbar.
- Show a compact 30/90 horizon selector next to the `预测线` switch when the card is hidden but the line is shown.

- [x] **Step 2: Gate loading and rendering**

- Wrap the forecast card with `v-if="showForecastCard"`.
- In `renderChart`, use `const fc = isDay && showForecastLine.value ? forecast.value : null`.
- On mount, call `loadForecast()` only when either toggle is on.
- When both toggles turn off, clear `forecast` and re-render.
- When either turns on and `forecast` is null, load it.

Run: `cd frontend; npm run lint && npm run build`
Expected: PASS.

---

### Task 6: Documentation and end-to-end checks

**Files:**
- Modify: `docs/10-design/05-接口契约.md`
- Modify: `docs/60-deliverables/用户手册.md`
- Modify: `e2e_m7.py`

- [x] **Step 1: Update the API contract**

Document the optional JSON body for `POST /api/v1/forecasts/recompute` and the 30/60/90-day risk-window rule.

- [x] **Step 2: Update the user manual**

Document selective recompute, the risk window rule, AI rationale expansion, and the metric detail display toggles.

- [x] **Step 3: Relax the risk superset assertion**

In `e2e_m7.py`, keep `far["count"] >= risk["count"]` (the union-over-horizons rule preserves it), and add an assertion that every risk item has a `horizonDays` in `{30, 90}`.

- [x] **Step 4: Run verification**

```bash
go test ./backend/... -count=1
cd frontend && npm run lint && npm run build
```

Expected: all pass.

---

## Self-Review

- Spec coverage: scoped recompute (Task 1/4), risk-window unification (Task 2), AI rationale/suggestion (Task 3), metric detail toggles (Task 5), docs/e2e (Task 6).
- No new routes or dependencies; full-batch recompute compatibility is preserved.
- Type names used consistently: `RunFilter`, `RunSummary`, `RunScopedResult`, `attachAIAdjustments`, `riskHorizonForWindow`, `selectRiskRow`.
