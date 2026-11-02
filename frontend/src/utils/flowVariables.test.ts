import assert from "node:assert/strict"
import test from "node:test"

import { pageAfterDelete, validateFlowVariableName } from "./flowVariables.ts"

test("rejects a custom variable that duplicates a builtin name", () => {
  const message = validateFlowVariableName("biz_date", [])
  assert.match(message, /内置变量/)
})

test("allows uppercase letters, digits, and underscores", () => {
  assert.equal(validateFlowVariableName("ClusterCode_2026", []), "")
  assert.equal(validateFlowVariableName("REGION_01", []), "")
})

test("rejects names outside the placeholder grammar", () => {
  assert.match(validateFlowVariableName("cluster-code", []), /大小写字母、数字和下划线/)
})

test("rejects a duplicate custom variable name", () => {
  const message = validateFlowVariableName("cluster_code", [{ id: 7, name: "cluster_code" }])
  assert.match(message, /已存在/)
})

test("allows a valid unique custom variable name", () => {
  assert.equal(validateFlowVariableName("cluster_code", [{ id: 7, name: "env_code" }], 7), "")
})

test("moves to the previous page after deleting the last row on a page", () => {
  assert.equal(pageAfterDelete(3, 1), 2)
  assert.equal(pageAfterDelete(3, 2), 3)
  assert.equal(pageAfterDelete(1, 1), 1)
})
