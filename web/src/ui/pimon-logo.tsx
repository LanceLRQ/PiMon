import { useId } from 'react'

// PiMon 标志：描边与阴影取主题 token（--logo-stroke / --logo-shadow），琥珀为固定品牌色
export function PiMonLogo({ className }: { className?: string }) {
  const gid = useId()
  return (
    <svg className={className} viewBox="0 0 64 64" role="img" aria-label="PiMon">
      <defs>
        <radialGradient id={gid}>
          <stop offset="0" stopColor="#F2C289" stopOpacity=".55" />
          <stop offset="1" stopColor="#E8AB6A" stopOpacity="0" />
        </radialGradient>
      </defs>
      <circle cx="32" cy="25.5" r="15" fill={`url(#${gid})`} />
      <rect x="7" y="9" width="50" height="34" rx="7" style={{ stroke: 'var(--logo-stroke)' }} strokeWidth="4.5" fill="none" />
      <g stroke="#E8AB6A" strokeWidth="2.5" strokeLinejoin="round" fill="none">
        <path d="M32.00 15.00 A10 10 0 0 1 32.00 31.50 A10 10 0 0 1 32.00 15.00 Z" />
        <path d="M41.96 32.25 A10 10 0 0 1 27.67 24.00 A10 10 0 0 1 41.96 32.25 Z" />
        <path d="M22.04 32.25 A10 10 0 0 1 36.33 24.00 A10 10 0 0 1 22.04 32.25 Z" />
      </g>
      <ellipse style={{ fill: 'var(--logo-shadow)' }} cx="32" cy="55" rx="15" ry="3" />
    </svg>
  )
}
