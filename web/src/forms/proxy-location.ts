// 代理可用位置规则，与后端 instances.locationAllowed 保持一致：
// role 为 'hub' 表示实例运行在中枢本机，否则是设备角色（server、nas、dev、vps）。
//   - 中枢本机：hub、lan、any 都可用；
//   - 非 VPS 设备：lan 与 any；
//   - VPS 设备：只有 any。
export function proxyUsableAt(location: string, role: string): boolean {
  if (location === 'any') return true
  if (location === 'lan') return role !== 'vps'
  if (location === 'hub') return role === 'hub'
  return false
}
