import dayjs from "dayjs"

// 统一时间展示: 后端返回 RFC3339(UTC) 时间戳, 转为浏览器本地时区展示
// 解析失败时退化为原字符串截断, 避免页面空白
export function fmtTs(v: string | null | undefined): string {
  if (!v) return "-"
  const d = dayjs(v)
  return d.isValid() ? d.format("YYYY-MM-DD HH:mm:ss") : v.replace("T", " ").slice(0, 19)
}
