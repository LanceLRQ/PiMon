import { useParams } from 'react-router'
import { InstanceEditor } from './editor/InstanceEditor'

export function InstanceEditPage() {
  const { id } = useParams()
  return <InstanceEditor mode="edit" instanceId={id} />
}
