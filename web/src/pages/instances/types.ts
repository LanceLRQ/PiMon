import type { Instance, InstanceDetail } from '@/types/generated'

// GET /api/instances/{id} 的响应体：Instance 的字段与 config、problems、report 在同一层
// （tygo 把内嵌结构生成成了 Instance 字段，这里按实际 JSON 形状重新声明）
export type InstanceDetailView = Instance & Omit<InstanceDetail, 'Instance'>
