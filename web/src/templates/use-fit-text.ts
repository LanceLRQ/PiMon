import { useLayoutEffect, useRef, useState } from 'react'
import { fitFontSize } from './fit'

interface FitOptions {
  max: number
  min: number
  maxLines?: number
  /** 容器里还有别的内容占高度时，只让文字用其中一部分，默认 1 */
  heightRatio?: number
}

/**
 * 量取容器尺寸并算出放得下文本的字号（px）。容器尺寸未知（首帧、测试环境）时返回 undefined，
 * 由 CSS 的 token 字号兜底。用 fitStyle 把结果与主题 token 字号取较小值，保证只缩不放。
 */
export function useFitText<T extends HTMLElement>(text: string, opts: FitOptions) {
  const ref = useRef<T>(null)
  const [box, setBox] = useState<{ width: number; height: number } | null>(null)
  useLayoutEffect(() => {
    const el = ref.current
    if (!el) return
    const measure = () => {
      if (el.clientWidth > 0 && el.clientHeight > 0) setBox({ width: el.clientWidth, height: el.clientHeight })
    }
    measure()
    const ro = new ResizeObserver(measure)
    ro.observe(el)
    return () => ro.disconnect()
  }, [])
  const px = box
    ? fitFontSize(text, { ...box, height: box.height * (opts.heightRatio ?? 1), max: opts.max, min: opts.min, maxLines: opts.maxLines })
    : undefined
  return [ref, px] as const
}

/** 主题 token 字号与适配结果取较小值 */
export function fitStyle(token: string, px: number | undefined): { fontSize: string } | undefined {
  return px === undefined ? undefined : { fontSize: `min(var(${token}), ${px}px)` }
}
