export const BUILTIN_FLOW_VARIABLE_NAMES = [
  "biz_date",
  "prev_trading_day",
  "next_trading_day",
  "trading_day_offset",
  "system_code",
  "flow_code",
  "metric_code",
  "hour",
] as const

export interface FlowVariableNameRow {
  id: number
  name: string
}

export function validateFlowVariableName(
  name: string,
  rows: FlowVariableNameRow[],
  editingId?: number | null,
): string {
  const value = name.trim()
  if (!value) return "变量名不能为空"
  if (value.length > 64) return "变量名长度不能超过 64"
  if (!/^[A-Za-z0-9_]+$/.test(value)) {
    return "变量名仅支持大小写字母、数字和下划线，如 ClusterCode_2026"
  }
  if ((BUILTIN_FLOW_VARIABLE_NAMES as readonly string[]).includes(value)) {
    return `自定义变量名不能与内置变量 \${${value}} 重名`
  }
  if (rows.some((row) => row.name === value && row.id !== editingId)) {
    return `变量名 ${value} 已存在`
  }
  return ""
}

export function pageAfterDelete(page: number, currentItemCount: number): number {
  return currentItemCount === 1 && page > 1 ? page - 1 : page
}
