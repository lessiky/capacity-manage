import assert from "node:assert/strict"
import test from "node:test"

import { loadActiveMetricOptions } from "./metricOptions.ts"

test("loads every active metric across all pages", async () => {
  const allMetrics = Array.from({ length: 201 }, (_, i) => ({
    metricCode: `METRIC_${String(i + 1).padStart(3, "0")}`,
    name: `指标 ${i + 1}`,
    status: "active",
  }))
  const calls: { page: number; pageSize: number; status: string }[] = []

  const result = await loadActiveMetricOptions(async (params) => {
    calls.push(params)
    const start = (params.page - 1) * params.pageSize
    return {
      items: allMetrics.slice(start, start + params.pageSize),
      total: allMetrics.length,
    }
  })

  assert.deepEqual(calls, [
    { page: 1, pageSize: 200, status: "active" },
    { page: 2, pageSize: 200, status: "active" },
  ])
  assert.equal(result.length, 201)
  assert.equal(result[0]?.metricCode, "METRIC_001")
  assert.equal(result[200]?.metricCode, "METRIC_201")
})

test("keeps only active metrics returned by the API", async () => {
  const result = await loadActiveMetricOptions(async () => ({
    items: [
      { metricCode: "ACTIVE", name: "启用指标", status: "active" },
      { metricCode: "DISABLED", name: "停用指标", status: "disabled" },
    ],
    total: 2,
  }))

  assert.deepEqual(result, [{ metricCode: "ACTIVE", name: "启用指标", status: "active" }])
})
