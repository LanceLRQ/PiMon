import { LayoutEditor } from '@/editor/LayoutEditor'
import { PlaceholderPage } from '@/ui/placeholder-page'
import { ScreenManager } from './ScreenManager'

export function ScreensPage() {
  return <ScreenManager />
}

export function LayoutEditorPage() {
  return <LayoutEditor />
}

export function SchedulePage() {
  return <PlaceholderPage no="02" titleKey="pages.schedule" milestone="M1d" />
}

export function RemotePage() {
  return <PlaceholderPage no="02" titleKey="pages.remote" milestone="M1d" />
}
