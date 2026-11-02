export interface MetricTreeRoot {
  metricCode: string
  systemCode: string
  children?: MetricTreeRoot[]
}

export interface MetricCategoryGroup<T extends MetricTreeRoot = MetricTreeRoot> {
  category: string
  count: number
  roots: T[]
}

export interface MetricSystemBranch<T extends MetricTreeRoot = MetricTreeRoot> {
  systemCode: string
  systemName: string
  count: number
  categories: MetricCategoryGroup<T>[]
}

export interface MetricSystemOption {
  systemCode: string
  name: string
}

function countTreeNodes(node: MetricTreeRoot): number {
  return 1 + (node.children ?? []).reduce((sum, child) => sum + countTreeNodes(child), 0)
}

export function buildMetricSystemTree<T extends MetricTreeRoot>(
  categories: MetricCategoryGroup<T>[],
  systems: MetricSystemOption[],
): MetricSystemBranch<T>[] {
  const systemNames = new Map(systems.map((system) => [system.systemCode, system.name]))
  const branches = new Map<string, MetricSystemBranch<T>>()

  for (const group of categories) {
    for (const root of group.roots) {
      let branch = branches.get(root.systemCode)
      if (!branch) {
        branch = {
          systemCode: root.systemCode,
          systemName: systemNames.get(root.systemCode) ?? root.systemCode,
          count: 0,
          categories: [],
        }
        branches.set(root.systemCode, branch)
      }

      let category = branch.categories.find((item) => item.category === group.category)
      if (!category) {
        category = { category: group.category, count: 0, roots: [] }
        branch.categories.push(category)
      }

      const rootCount = countTreeNodes(root)
      category.roots.push(root)
      category.count += rootCount
      branch.count += rootCount
    }
  }

  return [...branches.values()]
}
