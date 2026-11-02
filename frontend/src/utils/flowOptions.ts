import { loadAllPages, type PageResult } from "./pagedOptions.ts"

export interface FlowOptionQuery {
  page: number
  pageSize: number
  status: "active"
}

export async function loadActiveFlowOptions<T>(
  fetchPage: (params: FlowOptionQuery) => Promise<PageResult<T>>,
): Promise<T[]> {
  return loadAllPages((page, pageSize) => fetchPage({ page, pageSize, status: "active" }))
}
