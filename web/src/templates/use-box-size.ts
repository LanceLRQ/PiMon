import { useLayoutEffect, useRef, useState } from 'react'

export interface BoxSize {
  width: number
  height: number
}

/** 量取容器尺寸；首帧与测试环境（jsdom 量不到）返回 null，由调用方用名义尺寸兜底 */
export function useBoxSize<T extends HTMLElement>() {
  const ref = useRef<T>(null)
  const [box, setBox] = useState<BoxSize | null>(null)
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
  return [ref, box] as const
}

/** 容器能放几行：量到尺寸按行高换算，量不到用名义容量 */
export function capacityOf(box: BoxSize | null, rowPx: number, nominal: number): number {
  return box ? Math.floor(box.height / rowPx) : nominal
}
