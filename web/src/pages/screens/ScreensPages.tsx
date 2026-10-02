import { LayoutEditor } from '@/editor/LayoutEditor'
import { RemotePage as RemoteView } from '@/pages/remote/RemotePage'
import { SchedulePage as ScheduleView } from '@/pages/schedule/SchedulePage'
import { ScreenManager } from './ScreenManager'

export function ScreensPage() {
  return <ScreenManager />
}

export function LayoutEditorPage() {
  return <LayoutEditor />
}

export function SchedulePage() {
  return <ScheduleView />
}

export function RemotePage() {
  return <RemoteView />
}
