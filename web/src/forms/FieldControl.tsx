import { ListControl, KvControl } from './CollectionControls'
import { LookupControl } from './LookupControl'
import type { ControlProps, ErrorMap, KvEntry, ListItem, LookupValue, SecretValue } from './model'
import { ProxyControl } from './ProxyControl'
import { BooleanControl, DurationControl, EnumControl, NumberControl, StringControl, TextControl } from './ScalarControls'
import { SecretControl } from './SecretControls'
import { UrlControl } from './UrlControl'

export interface FieldControlProps extends ControlProps {
  errors: ErrorMap
}

// 按字段类型选择输入组件（object_list 由 SchemaFields 处理，它需要递归渲染子字段）
export function FieldControl(props: FieldControlProps) {
  const { field, errors, ...rest } = props
  const p = { field, ...rest }
  switch (field.type) {
    case 'string':
      return <StringControl {...(p as ControlProps<string>)} />
    case 'text':
      return <TextControl {...(p as ControlProps<string>)} />
    case 'number':
      return <NumberControl {...(p as ControlProps<string>)} />
    case 'boolean':
      return <BooleanControl {...(p as ControlProps<boolean>)} />
    case 'enum':
      return <EnumControl {...(p as ControlProps<string>)} />
    case 'secret':
    case 'secret_url':
      return <SecretControl {...(p as ControlProps<SecretValue>)} />
    case 'url':
      return <UrlControl {...(p as ControlProps<string>)} />
    case 'proxy':
      return <ProxyControl {...(p as ControlProps<string>)} />
    case 'duration':
      return <DurationControl {...(p as ControlProps<string>)} />
    case 'list':
      return <ListControl {...(p as ControlProps<ListItem[]>)} errors={errors} />
    case 'kv':
      return <KvControl {...(p as ControlProps<KvEntry[]>)} errors={errors} />
    case 'lookup':
      return <LookupControl {...(p as ControlProps<LookupValue | null>)} />
    default:
      return null
  }
}
