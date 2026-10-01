import { createContext, useContext, useLayoutEffect, useRef, useState, type ReactNode } from 'react'
import { getTheme } from '@/themes/registry'
import { readThemeRuntime, watchThemeRuntime, type ThemeRuntime } from '@/themes/runtime'
import type { ThemeId, ThemePalette } from '@/themes/types'

/**
 * 主题运行时上下文：五级 marker/weight 等关键字型 token 在主题切换时由 getComputedStyle 读出（Ruling 13）。
 * 没有 ThemeRoot 时回落到文档根的读数。
 */
export const ThemeContext = createContext<ThemeRuntime | null>(null)

interface ThemeRootProps {
  themeId: ThemeId
  reduceEffects?: boolean
  className?: string
  children: ReactNode
}

/**
 * 主题根：data-theme 与 data-reduce-effects 写在同一元素上，主题或降低特效变化时重新读取运行时数据。
 * 屏幕根与编辑器画布用它包住小组件。
 */
export function ThemeRoot({ themeId, reduceEffects = false, className, children }: ThemeRootProps) {
  const ref = useRef<HTMLDivElement>(null)
  const [runtime, setRuntime] = useState<ThemeRuntime | null>(null)
  useLayoutEffect(() => {
    const el = ref.current
    if (!el) return
    setRuntime(readThemeRuntime(el))
    return watchThemeRuntime(el, setRuntime)
  }, [themeId, reduceEffects])
  return (
    <div
      ref={ref}
      data-theme={themeId}
      data-reduce-effects={reduceEffects ? '' : undefined}
      className={className}
    >
      <ThemeContext.Provider value={runtime}>{children}</ThemeContext.Provider>
    </div>
  )
}

export function useThemeRuntime(): ThemeRuntime {
  return useContext(ThemeContext) ?? readThemeRuntime()
}

/** 色板能力：M1 只实现 full 分支，其余档位暂按 full 渲染 */
export function useThemePalette(): ThemePalette {
  return getTheme(useThemeRuntime().themeId).palette
}
