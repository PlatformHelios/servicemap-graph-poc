import { ChevronLeft, ChevronRight } from "lucide-react";

// Lists read one page at a time from the API (the graph is expected to grow
// to millions of records). The default page size keeps a table scannable.
export const defaultPageSize = 50;

// Clamp a page index so it still points inside a result set whose size changed.
export function clampPage(page: number, total: number, pageSize = defaultPageSize) {
  const lastPage = Math.max(0, Math.ceil(total / pageSize) - 1);
  return Math.min(Math.max(0, page), lastPage);
}

// "Showing 1–50 of 1,204 records" with previous/next controls; renders the
// plain count while a result set fits on one page.
export function Pager({ page, total, pageSize = defaultPageSize, loading, noun, onPage }: {
  page: number;
  total: number;
  pageSize?: number;
  loading?: boolean;
  noun: string; // singular, e.g. "record" or "task"
  onPage: (page: number) => void;
}) {
  const pages = Math.max(1, Math.ceil(total / pageSize));
  const first = total === 0 ? 0 : page * pageSize + 1;
  const last = Math.min(total, (page + 1) * pageSize);
  const plural = `${noun}s`;
  if (loading) return <span>Loading {plural}…</span>;
  if (total <= pageSize) return <span>{total.toLocaleString()} {total === 1 ? noun : plural}</span>;
  return <span className="pager">
    <span>Showing {first.toLocaleString()}–{last.toLocaleString()} of {total.toLocaleString()} {plural}</span>
    <button type="button" className="action-button" onClick={() => onPage(page - 1)} disabled={page === 0} aria-label="Previous page"><ChevronLeft size={13} /><span>Prev</span></button>
    <span>Page {page + 1} of {pages}</span>
    <button type="button" className="action-button" onClick={() => onPage(page + 1)} disabled={page + 1 >= pages} aria-label="Next page"><span>Next</span><ChevronRight size={13} /></button>
  </span>;
}
