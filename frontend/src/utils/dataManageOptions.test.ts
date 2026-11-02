import assert from "node:assert/strict"
import test from "node:test"

import { loadAllMetricOptions } from "./metricOptions.ts"
import { loadActiveFlowOptions } from "./flowOptions.ts"

test("loads every metric for data management, including disabled metrics", async () => {
  const allMetrics = Array.from({ length: 201 }, (_, i) => ({
    metricCode: `METRIC_${String(i + 1).padStart(3, "0")}`,
    name: `指标 ${i + 1}`,
    status: i === 0 ? "disabled" : "active",
  }))
  const calls: { page: number; pageSize: number }[] = []

  const result = await loadAllMetricOptions(async (params) => {
    calls.push(params)
    const start = (params.page - 1) * params.pageSize
    return {
      items: allMetrics.slice(start, start + params.pageSize),
      total: allMetrics.length,
    }
  })

  assert.deepEqual(calls, [
    { page: 1, pageSize: 200 },
    { page: 2, pageSize: 200 },
  ])
  assert.equal(result.length, 201)
  assert.equal(result[0]?.status, "disabled")
  assert.equal(result[200]?.metricCode, "METRIC_201")
})

test("loads every active flow across all pages for historical backfill", async () => {
  const allFlows = Array.from({ length: 201 }, (_, i) => ({
    id: i + 1,
    flowCode: `FLOW_${String(i + 1).padStart(3, "0")}`,
    name: `流程 ${i + 1}`,
    metricCode: `METRIC_${String(i + 1).padStart(3, "0")}`,
  }))
  const calls: { page: number; pageSize: number; status: string }[] = []

  const result = await loadActiveFlowOptions(async (params) => {
    calls.push(params)
    const start = (params.page - 1) * params.pageSize
    return {
      items: allFlows.slice(start, start + params.pageSize),
      total: allFlows.length,
    }
  })

  assert.deepEqual(calls, [
    { page: 1, pageSize: 200, status: "active" },
    { page: 2, pageSize: 200, status: "active" },
  ])
  assert.equal(result.length, 201)
  assert.equal(result[0]?.flowCode, "FLOW_001")
  assert.equal(result[200]?.flowCode, "FLOW_201")
})
