import { useState, useEffect, useMemo } from 'react'
import { useLocation, useNavigate, Link } from 'react-router-dom'
import { cn, copyToClipboard } from '@/lib/utils'
import { Button, CopyButton } from '@/components/ui'
import { Mermaid } from '@/components/ui/Mermaid'
import {
  docsSections, findDocsPage, getDefaultPage, getAdjacentPages, generateFullDocsMarkdown,
} from '@/components/docs/content'
import {
  BookOpen, ChevronRight, ChevronDown, ArrowLeft, ArrowRight,
  Menu, X, Copy, Check, Download, Bot,
} from 'lucide-react'

/* ─── Markdown renderer (block-based, returns segments for React rendering) ─── */
type Segment = { type: 'html'; content: string } | { type: 'mermaid'; content: string }

function parseMarkdown(md: string): Segment[] {
  const segments: Segment[] = []
  const htmlBlocks: string[] = []

  function flushHtml() {
    if (htmlBlocks.length > 0) {
      segments.push({ type: 'html', content: htmlBlocks.join('\n') })
      htmlBlocks.length = 0
    }
  }

  const lines = md.split('\n')
  let i = 0

  while (i < lines.length) {
    const line = lines[i]

    // Code blocks — detect language
    if (line.startsWith('```')) {
      const lang = line.slice(3).trim()
      const codeLines: string[] = []
      i++
      while (i < lines.length && !lines[i].startsWith('```')) {
        codeLines.push(lines[i])
        i++
      }
      i++ // skip closing ```

      if (lang === 'mermaid') {
        flushHtml()
        segments.push({ type: 'mermaid', content: codeLines.join('\n').trim() })
      } else {
        const escaped = codeLines.join('\n').replace(/</g, '&lt;').replace(/>/g, '&gt;').trim()
        htmlBlocks.push(`<div class="relative group my-4"><pre class="bg-background border border-border rounded-sm p-4 overflow-x-auto"><code class="text-xs font-mono text-foreground leading-relaxed">${escaped}</code></pre><button class="doc-copy-btn absolute top-2 right-2 text-muted-foreground/50 hover:text-primary opacity-0 group-hover:opacity-100 transition-opacity p-1" data-code="${encodeURIComponent(escaped)}" aria-label="Copy code"><svg xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect width="14" height="14" x="8" y="8" rx="2" ry="2"/><path d="M4 16c-1.1 0-2-.9-2-2V4c0-1.1.9-2 2-2h10c1.1 0 2 .9 2 2"/></svg></button></div>`)
      }
      continue
    }

    // Image tags: ![alt](src)
    const imgMatch = line.match(/^!\[([^\]]*)\]\(([^)]+)\)$/)
    if (imgMatch) {
      htmlBlocks.push(`<img src="${imgMatch[2]}" alt="${imgMatch[1]}" class="my-6 rounded-sm border border-border max-w-full" />`)
      i++; continue
    }

    // Tables — collect consecutive | lines
    if (line.startsWith('|')) {
      const tableRows: string[][] = []
      let hasHeader = false
      while (i < lines.length && lines[i].startsWith('|')) {
        const row = lines[i].split('|').filter(c => c.trim())
        if (row.every(c => /^[\s\-:]+$/.test(c))) { hasHeader = true; i++; continue }
        tableRows.push(row.map(c => c.trim()))
        i++
      }
      if (tableRows.length > 0) {
        let tableHtml = '<div class="overflow-x-auto my-4"><table class="w-full text-sm">'
        tableRows.forEach((row, ri) => {
          const tag = (hasHeader && ri === 0) ? 'th' : 'td'
          const cls = tag === 'th'
            ? 'px-3 py-2 text-xs font-medium text-foreground border-b border-border text-left'
            : 'px-3 py-2 text-xs text-muted-foreground border-b border-border'
          tableHtml += '<tr>' + row.map(c => `<${tag} class="${cls}">${renderInline(c)}</${tag}>`).join('') + '</tr>'
        })
        tableHtml += '</table></div>'
        htmlBlocks.push(tableHtml)
      }
      continue
    }

    // Headers
    const h1 = line.match(/^# (.+)$/)
    if (h1) { htmlBlocks.push(`<h1 class="text-xl font-bold text-foreground mb-6">${renderInline(h1[1])}</h1>`); i++; continue }
    const h2 = line.match(/^## (.+)$/)
    if (h2) { htmlBlocks.push(`<h2 class="text-base font-semibold text-foreground mt-10 mb-4" id="${h2[1]}">${renderInline(h2[1])}</h2>`); i++; continue }
    const h3 = line.match(/^### (.+)$/)
    if (h3) { htmlBlocks.push(`<h3 class="text-sm font-semibold text-foreground mt-8 mb-3">${renderInline(h3[1])}</h3>`); i++; continue }

    // Blockquotes
    if (line.startsWith('> ')) {
      htmlBlocks.push(`<blockquote class="border-l-2 border-primary pl-4 text-sm text-muted-foreground italic my-4">${renderInline(line.slice(2))}</blockquote>`)
      i++; continue
    }

    // Horizontal rules
    if (/^---$/.test(line)) { htmlBlocks.push('<hr class="border-border my-8" />'); i++; continue }

    // Unordered lists
    if (line.match(/^- /)) {
      let listHtml = '<ul class="my-3 space-y-1">'
      while (i < lines.length && lines[i].match(/^- /)) {
        listHtml += `<li class="text-sm text-muted-foreground ml-4 list-disc">${renderInline(lines[i].slice(2))}</li>`
        i++
      }
      listHtml += '</ul>'
      htmlBlocks.push(listHtml)
      continue
    }

    // Ordered lists
    if (line.match(/^\d+\. /)) {
      let listHtml = '<ol class="my-3 space-y-1">'
      while (i < lines.length && lines[i].match(/^\d+\. /)) {
        const content = lines[i].replace(/^\d+\. /, '')
        listHtml += `<li class="text-sm text-muted-foreground ml-4 list-decimal">${renderInline(content)}</li>`
        i++
      }
      listHtml += '</ol>'
      htmlBlocks.push(listHtml)
      continue
    }

    // Empty lines
    if (line.trim() === '') { i++; continue }

    // Paragraphs
    htmlBlocks.push(`<p class="text-sm text-muted-foreground leading-relaxed mb-3">${renderInline(line)}</p>`)
    i++
  }

  flushHtml()
  return segments
}

function renderInline(text: string): string {
  return text
    .replace(/`([^`]+)`/g, '<code class="bg-secondary px-1.5 py-0.5 rounded text-xs font-mono text-primary">$1</code>')
    .replace(/\*\*(.+?)\*\*/g, '<strong class="text-foreground font-medium">$1</strong>')
    .replace(/\*(.+?)\*/g, '<em>$1</em>')
    .replace(/\[([^\]]+)\]\(([^)]+)\)/g, '<a href="$2" class="text-primary hover:underline">$1</a>')
}

function RenderedContent({ segments }: { segments: Segment[] }) {
  return (
    <>
      {segments.map((seg, i) =>
        seg.type === 'mermaid' ? (
          <Mermaid key={i} chart={seg.content} className="my-6 overflow-x-auto" />
        ) : (
          <div key={i} dangerouslySetInnerHTML={{ __html: seg.content }} />
        )
      )}
    </>
  )
}

/* ─── TOC extractor ─── */
function extractTOC(md: string): { id: string; text: string; level: number }[] {
  const headings: { id: string; text: string; level: number }[] = []
  const lines = md.split('\n')
  for (const line of lines) {
    const m2 = line.match(/^## (.+)$/)
    const m3 = line.match(/^### (.+)$/)
    if (m2) headings.push({ id: m2[1], text: m2[1], level: 2 })
    if (m3) headings.push({ id: m3[1], text: m3[1], level: 3 })
  }
  return headings
}

/* ─── Docs Sidebar ─── */
function DocsSidebar({ activeSectionId, activePageId, onNavigate, className }: {
  activeSectionId: string; activePageId: string; onNavigate: (s: string, p: string) => void; className?: string
}) {
  const [openSections, setOpenSections] = useState<Set<string>>(new Set([activeSectionId]))

  // Keep active section open when it changes
  useEffect(() => {
    setOpenSections(prev => {
      const next = new Set(prev)
      next.add(activeSectionId)
      return next
    })
  }, [activeSectionId])

  const toggleSection = (id: string) => {
    setOpenSections(prev => {
      const next = new Set(prev)
      next.has(id) ? next.delete(id) : next.add(id)
      return next
    })
  }

  return (
    <div className={cn('space-y-1', className)}>
      {docsSections.map(section => (
        <div key={section.id}>
          <button
            onClick={() => toggleSection(section.id)}
            className="w-full flex items-center gap-2 px-2 py-1.5 text-xs font-medium text-muted-foreground hover:text-foreground transition-colors"
            aria-expanded={openSections.has(section.id)}
          >
            <ChevronRight className={cn('w-3 h-3 transition-transform', openSections.has(section.id) && 'rotate-90')} />
            {section.title}
          </button>
          {openSections.has(section.id) && (
            <div className="ml-4 space-y-0.5">
              {section.pages.map(page => (
                <button
                  key={page.id}
                  onClick={() => onNavigate(section.id, page.id)}
                  className={cn(
                    'w-full text-left px-3 py-1.5 text-xs rounded-sm transition-colors',
                    activeSectionId === section.id && activePageId === page.id
                      ? 'bg-primary/10 text-primary font-medium'
                      : 'text-muted-foreground hover:text-foreground hover:bg-secondary',
                  )}
                >
                  {page.title}
                </button>
              ))}
            </div>
          )}
        </div>
      ))}
    </div>
  )
}

/* ─── LLM Page ─── */
function LLMPage() {
  const [copied, setCopied] = useState(false)
  const fullDocs = useMemo(() => generateFullDocsMarkdown(), [])

  const handleCopy = async () => {
    await copyToClipboard(fullDocs)
    setCopied(true)
    setTimeout(() => setCopied(false), 3000)
  }

  const handleDownload = () => {
    const blob = new Blob([fullDocs], { type: 'text/markdown' })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = 'gradient-docs.md'
    a.click()
    URL.revokeObjectURL(url)
  }

  return (
    <div className="max-w-3xl">
      <h1 className="text-xl font-bold text-foreground mb-4 flex items-center gap-2">
        <Bot className="w-6 h-6 text-primary" />
        LLM-Friendly Documentation
      </h1>
      <p className="text-sm text-muted-foreground mb-6 leading-relaxed">
        This page contains the complete Gradient documentation in a single Markdown document,
        optimized for pasting into an LLM context window (Claude, ChatGPT, Cursor, etc.).
      </p>

      <div className="flex flex-wrap gap-3 mb-8">
        <Button onClick={handleCopy}>
          {copied ? <Check className="w-4 h-4" /> : <Copy className="w-4 h-4" />}
          {copied ? 'Copied to clipboard!' : 'Copy all docs for LLM'}
        </Button>
        <Button variant="secondary" onClick={handleDownload}>
          <Download className="w-4 h-4" />
          Download as .md
        </Button>
      </div>

      <div className="bg-card border border-border rounded-sm">
        <div className="flex items-center justify-between px-4 py-2 border-b border-border">
          <span className="text-xs text-muted-foreground">gradient-docs.md</span>
          <span className="text-[10px] text-muted-foreground/50">{(fullDocs.length / 1024).toFixed(1)} KB · {fullDocs.split('\n').length} lines</span>
        </div>
        <pre className="p-4 max-h-[600px] overflow-auto text-xs font-mono text-muted-foreground leading-relaxed whitespace-pre-wrap">
          {fullDocs}
        </pre>
      </div>

      <div className="mt-6 bg-primary/10 border border-primary/20 rounded-sm p-4">
        <p className="text-xs text-primary font-medium mb-1">💡 Usage tip</p>
        <p className="text-xs text-muted-foreground">
          Paste this into your LLM&apos;s system prompt or context window, then ask questions like:
          &ldquo;How do I set up autoscaling?&rdquo; or &ldquo;What&apos;s the API endpoint for publishing context events?&rdquo;
        </p>
      </div>
    </div>
  )
}

/* ─── Main Docs Page ─── */
export default function DocsPage() {
  const location = useLocation()
  const navigate = useNavigate()
  const [mobileNavOpen, setMobileNavOpen] = useState(false)

  // Parse URL
  const pathParts = location.pathname.replace('/docs', '').split('/').filter(Boolean)
  const isLLMPage = pathParts[0] === 'llm'
  const sectionId = pathParts[0] || getDefaultPage().sectionId
  const pageId = pathParts[1] || (sectionId === getDefaultPage().sectionId ? getDefaultPage().pageId : docsSections.find(s => s.id === sectionId)?.pages[0]?.id || '')

  const currentPage = findDocsPage(sectionId, pageId)
  const { prev, next } = getAdjacentPages(sectionId, pageId)
  const toc = currentPage ? extractTOC(currentPage.content) : []
  const contentSegments = useMemo(() => currentPage ? parseMarkdown(currentPage.content) : [], [currentPage])

  // Copy code buttons
  useEffect(() => {
    const handler = (e: MouseEvent) => {
      const btn = (e.target as HTMLElement).closest('.doc-copy-btn') as HTMLButtonElement | null
      if (btn) {
        const code = decodeURIComponent(btn.dataset.code || '')
          .replace(/&lt;/g, '<').replace(/&gt;/g, '>')
        copyToClipboard(code)
        btn.classList.add('text-primary')
        setTimeout(() => btn.classList.remove('text-primary'), 2000)
      }
    }
    document.addEventListener('click', handler)
    return () => document.removeEventListener('click', handler)
  }, [])

  const handleNavigate = (s: string, p: string) => {
    navigate(`/docs/${s}/${p}`)
    setMobileNavOpen(false)
    window.scrollTo(0, 0)
  }

  return (
    <div className="min-h-screen bg-background">
      <a href="#docs-content" className="skip-link">Skip to content</a>

      {/* Top nav */}
      <header className="sticky top-0 z-50 bg-background/95 backdrop-blur-sm border-b border-border">
        <div className="max-w-7xl mx-auto px-4 h-14 flex items-center justify-between">
          <div className="flex items-center gap-4">
            <button
              className="md:hidden p-1.5 text-muted-foreground hover:text-foreground"
              onClick={() => setMobileNavOpen(!mobileNavOpen)}
              aria-label="Toggle docs navigation"
            >
              {mobileNavOpen ? <X className="w-5 h-5" /> : <Menu className="w-5 h-5" />}
            </button>
            <Link to="/" className="flex items-center gap-2 text-foreground hover:text-primary transition-colors">
              <img src="/logo.svg" alt="Gradient" className="h-6 w-auto" />
              <span className="text-sm font-semibold">Gradient</span>
            </Link>
            <span className="text-muted-foreground/40 text-xs">/</span>
            <span className="text-sm text-muted-foreground font-medium flex items-center gap-1"><BookOpen className="w-3.5 h-3.5" /> Docs</span>
          </div>
          <div className="flex items-center gap-3">
            <Link to="/dashboard" className="text-xs text-muted-foreground hover:text-foreground transition-colors">Dashboard</Link>
          </div>
        </div>
      </header>

      <div className="max-w-7xl mx-auto flex">
        {/* Sidebar */}
        <aside
          className={cn(
            'fixed md:sticky top-14 z-40 h-[calc(100vh-3.5rem)] w-64 border-r border-border bg-background p-4 overflow-y-auto transition-transform',
            mobileNavOpen ? 'translate-x-0' : '-translate-x-full md:translate-x-0',
          )}
          role="navigation"
          aria-label="Documentation navigation"
        >
          <DocsSidebar
            activeSectionId={sectionId}
            activePageId={pageId}
            onNavigate={handleNavigate}
          />
        </aside>

        {/* Mobile overlay */}
        {mobileNavOpen && (
          <div className="fixed inset-0 z-30 bg-background/80 md:hidden" onClick={() => setMobileNavOpen(false)} />
        )}

        {/* Content */}
        <main id="docs-content" className="flex-1 min-w-0 px-6 md:px-10 py-8">
          {isLLMPage ? (
            <LLMPage />
          ) : currentPage ? (
            <div className="max-w-3xl">
              {/* Breadcrumb */}
              <nav className="flex items-center gap-1.5 text-[10px] text-muted-foreground/50 mb-6" aria-label="Breadcrumb">
                <Link to="/docs" className="hover:text-foreground transition-colors">Docs</Link>
                <ChevronRight className="w-3 h-3" />
                <span>{docsSections.find(s => s.id === sectionId)?.title}</span>
                <ChevronRight className="w-3 h-3" />
                <span className="text-muted-foreground">{currentPage.title}</span>
              </nav>

              {/* Copy page for LLM */}
              <div className="flex justify-end mb-4">
                <CopyButton
                  text={currentPage.content}
                  label="Copy page as markdown"
                  className="text-[10px]"
                />
              </div>

              {/* Rendered content */}
              <article>
                <RenderedContent segments={contentSegments} />
              </article>

              {/* Prev/Next */}
              <nav className="flex items-center justify-between mt-12 pt-6 border-t border-border" aria-label="Page navigation">
                {prev ? (
                  <button
                    onClick={() => handleNavigate(prev.sectionId, prev.pageId)}
                    className="flex items-center gap-2 text-xs text-muted-foreground hover:text-primary transition-colors"
                  >
                    <ArrowLeft className="w-3.5 h-3.5" />
                    {prev.title}
                  </button>
                ) : <div />}
                {next ? (
                  <button
                    onClick={() => handleNavigate(next.sectionId, next.pageId)}
                    className="flex items-center gap-2 text-xs text-muted-foreground hover:text-primary transition-colors"
                  >
                    {next.title}
                    <ArrowRight className="w-3.5 h-3.5" />
                  </button>
                ) : <div />}
              </nav>
            </div>
          ) : (
            <div className="text-center py-20">
              <BookOpen className="w-8 h-8 text-muted-foreground/50 mx-auto mb-4" />
              <h2 className="text-sm font-medium text-foreground mb-1">Page not found</h2>
              <p className="text-xs text-muted-foreground mb-4">This documentation page doesn&apos;t exist yet.</p>
              <Button variant="secondary" onClick={() => handleNavigate('getting-started', 'introduction')}>
                Go to Introduction
              </Button>
            </div>
          )}
        </main>

        {/* Right TOC */}
        {!isLLMPage && toc.length > 0 && (
          <aside className="hidden xl:block sticky top-14 h-[calc(100vh-3.5rem)] w-48 p-4 overflow-y-auto" aria-label="Table of contents">
            <p className="text-[10px] font-medium text-muted-foreground uppercase tracking-wider mb-3">On this page</p>
            <ul className="space-y-1.5">
              {toc.map(h => (
                <li key={h.id}>
                  <a
                    href={`#${h.id}`}
                    className={cn(
                      'block text-[11px] text-muted-foreground/50 hover:text-foreground transition-colors truncate',
                      h.level === 3 && 'ml-3',
                    )}
                  >
                    {h.text}
                  </a>
                </li>
              ))}
            </ul>
          </aside>
        )}
      </div>
    </div>
  )
}
