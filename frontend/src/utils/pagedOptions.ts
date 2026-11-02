export interface PageResult<T> {
  items: T[]
  total: number
}

export const OPTION_PAGE_SIZE = 200

export async function loadAllPages<T>(
  fetchPage: (page: number, pageSize: number) => Promise<PageResult<T>>,
): Promise<T[]> {
  const first = await fetchPage(1, OPTION_PAGE_SIZE)
  const items = [...first.items]
  const pages = Math.ceil(first.total / OPTION_PAGE_SIZE)

  for (let page = 2; page <= pages; page += 1) {
    const next = await fetchPage(page, OPTION_PAGE_SIZE)
    items.push(...next.items)
  }

  return items
}
