export type FileShortcut = 'selectAll' | 'rename' | 'create' | 'refresh' | 'up' | 'back' | 'forward' | 'menu' | 'clear'
type Key = Pick<KeyboardEvent,'key'|'ctrlKey'|'altKey'|'shiftKey'|'metaKey'|'isComposing'|'defaultPrevented'>

// Only classify intended file commands. Text editing and IME composition keep
// their native keys; the component separately checks page and operation locks.
export function fileShortcut(event: Key, editing: boolean): FileShortcut | null {
  if (editing || event.isComposing || event.defaultPrevented || event.metaKey) return null
  const key = event.key.toLowerCase()
  if (event.ctrlKey && !event.altKey) {
    if (key === 'a' && !event.shiftKey) return 'selectAll'
    if (key === 'n' && event.shiftKey) return 'create'
    return null
  }
  if (event.altKey) return !event.ctrlKey && !event.shiftKey ? ({arrowup:'up',arrowleft:'back',arrowright:'forward'} as Record<string,FileShortcut>)[key]??null : null
  if (event.ctrlKey) return null
  if (event.shiftKey) return key === 'f10' ? 'menu' : null
  return ({f2:'rename',f5:'refresh',escape:'clear'} as Record<string,FileShortcut>)[key] ?? null
}
