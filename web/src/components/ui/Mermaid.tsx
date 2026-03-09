import { useEffect, useRef, useState } from 'react'
import mermaid from 'mermaid'

mermaid.initialize({
  startOnLoad: false,
  theme: 'dark',
  themeVariables: {
    darkMode: true,
    background: '#08090a',
    primaryColor: '#1a2e2b',
    primaryTextColor: '#b4bcd0',
    primaryBorderColor: '#5a9a94',
    secondaryColor: '#0d0e10',
    secondaryTextColor: '#5c6578',
    secondaryBorderColor: '#1a1c20',
    tertiaryColor: '#0d0e10',
    lineColor: '#5a9a94',
    textColor: '#b4bcd0',
    mainBkg: '#0d0e10',
    nodeBorder: '#5a9a94',
    clusterBkg: '#08090a',
    clusterBorder: '#1a1c20',
    titleColor: '#b4bcd0',
    edgeLabelBackground: '#08090a',
    nodeTextColor: '#b4bcd0',
    actorTextColor: '#b4bcd0',
    actorLineColor: '#5a9a94',
    signalColor: '#b4bcd0',
    signalTextColor: '#b4bcd0',
    fontFamily: "'Space Grotesk', system-ui, sans-serif",
    fontSize: '13px',
  },
  flowchart: { curve: 'basis', padding: 16 },
  securityLevel: 'loose',
})

let idCounter = 0

export function Mermaid({ chart, className }: { chart: string; className?: string }) {
  const containerRef = useRef<HTMLDivElement>(null)
  const [svg, setSvg] = useState('')
  const idRef = useRef(`mermaid-${++idCounter}`)

  useEffect(() => {
    let cancelled = false
    mermaid.render(idRef.current, chart.trim()).then(({ svg }) => {
      if (!cancelled) setSvg(svg)
    }).catch((err) => {
      console.error('[mermaid] render error:', err)
    })
    return () => { cancelled = true }
  }, [chart])

  return (
    <div
      ref={containerRef}
      className={className}
      dangerouslySetInnerHTML={{ __html: svg }}
    />
  )
}
