import assert from "node:assert/strict"
import test from "node:test"

import { buildMetricSystemTree } from "./metricTree.ts"

test("splits same-named categories into separate system branches", () => {
  const result = buildMetricSystemTree(
    [{
      category: "计算",
      count: 3,
      roots: [
        {
          metricCode: "CPU",
          systemCode: "S1",
          children: [{ metricCode: "CPU.USAGE", systemCode: "S1" }],
        },
        { metricCode: "MEM", systemCode: "S2" },
      ],
    }],
    [
      { systemCode: "S1", name: "核心系统" },
      { systemCode: "S2", name: "支付系统" },
    ],
  )

  assert.deepEqual(result, [
    {
      systemCode: "S1",
      systemName: "核心系统",
      count: 2,
      categories: [{
        category: "计算",
        count: 2,
        roots: [{
          metricCode: "CPU",
          systemCode: "S1",
          children: [{ metricCode: "CPU.USAGE", systemCode: "S1" }],
        }],
      }],
    },
    {
      systemCode: "S2",
      systemName: "支付系统",
      count: 1,
      categories: [{
        category: "计算",
        count: 1,
        roots: [{ metricCode: "MEM", systemCode: "S2" }],
      }],
    },
  ])
})
