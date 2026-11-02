import { loadAllPages, type PageResult } from "./pagedOptions.ts"

export interface MetricOption {
  metricCode: string
  name: string
  status: string
}

export interface MetricOptionPage {
  items: MetricOption[]
  total: number
}

export interface MetricOptionQuery {
  page: number
  pageSize: number
  status: "active"
}

export interface AllMetricOptionQuery {
  page: number
  pageSize: number
}

export async function loadActiveMetricOptions(
  fetchPage: (params: MetricOptionQuery) => Promise<PageResult<MetricOption>>,
): Promise<MetricOption[]> {
  return loadAllPages(async (page, pageSize) => {
    const result = await fetchPage({ page, pageSize, status: "active" })
    return {
      items: result.items.filter((metric) => metric.status === "active"),
      total: result.total,
    }
  })
}

export async function loadAllMetricOptions(
  fetchPage: (params: AllMetricOptionQuery) => Promise<MetricOptionPage>,
): Promise<MetricOption[]> {
  return loadAllPages((page, pageSize) => fetchPage({ page, pageSize }))
}
