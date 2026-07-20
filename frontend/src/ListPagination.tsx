import { ChevronLeft, ChevronRight } from 'lucide-react'

export const LIST_PAGE_SIZE = 50

export function ListPagination({ page, pageSize = LIST_PAGE_SIZE, total, label, onPageChange }: {
  page: number
  pageSize?: number
  total: number
  label: string
  onPageChange: (page: number) => void
}) {
  if (total <= pageSize) return null
  const pageCount = Math.ceil(total / pageSize)
  const rangeStart = (page - 1) * pageSize + 1
  const rangeEnd = Math.min(total, page * pageSize)

  return <nav className="list-pagination" aria-label={label}>
    <span aria-live="polite">第 {page} / {pageCount} 页 · 显示 {rangeStart}–{rangeEnd}，共 {total} 条</span>
    <div>
      <button className="secondary" disabled={page <= 1} onClick={() => onPageChange(page - 1)}><ChevronLeft/>上一页</button>
      <button className="secondary" disabled={page >= pageCount} onClick={() => onPageChange(page + 1)}>下一页<ChevronRight/></button>
    </div>
  </nav>
}
