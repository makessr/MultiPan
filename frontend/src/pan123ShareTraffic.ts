export const pan123PermissionOptions = [
  {value: 1, label: '关闭两项提取权限'},
  {value: 2, label: '游客免登录提取'},
  {value: 3, label: '免费用户提取'},
  {value: 4, label: '游客免登录及免费用户提取'},
]

export function pan123TrafficLimit(mode: string, amount: string, unit: number): number | null {
  if (mode === 'unlimited') return 0
  if (mode !== 'limit' || !/^\d+$/.test(amount)) return null
  const bytes = Number(amount) * unit
  return Number.isSafeInteger(bytes) && bytes > 0 && bytes <= 2**50 ? bytes : null
}
