export type FileSort = 'asc' | 'desc' | 'size-asc' | 'size-desc' | 'type-asc' | 'type-desc'
export type DisplayKind = 'all' | 'files' | 'folders'
export type ListEntry = { id: string; name: string; folder: boolean; size?: number | null; sizeText?: string }
const collator = new Intl.Collator('zh-CN', {numeric:true, sensitivity:'base'})

export function fileExtension(entry: ListEntry): string {
  if (entry.folder) return ''
  const dot = entry.name.lastIndexOf('.')
  return dot > 0 && dot < entry.name.length - 1 ? entry.name.slice(dot + 1).toLocaleLowerCase() : ''
}
export function fileType(entry: ListEntry): string {
  if (entry.folder) return '文件夹'
  const extension = fileExtension(entry)
  return extension ? extension.toLocaleUpperCase() + ' 文件' : '文件'
}

// For list ordering only. Lanzou's display size is approximate; never use this
// value for download lengths, hashes, upload verification or protocol requests.
export function displaySize(entry: ListEntry): number | null {
  if (entry.folder) return null
  if (entry.sizeText?.trim()) {
    const match = /^([0-9]+(?:\.[0-9]+)?)\s*(?:([KMGT])(?:I?B)?|B)?$/i.exec(entry.sizeText.trim())
    if (!match) return null
    const unit = ['', 'K', 'M', 'G', 'T'].indexOf(match[2]?.toUpperCase()??'')
    const value = Number(match[1]) * 1024 ** unit
    return Number.isFinite(value) && value <= Number.MAX_SAFE_INTEGER ? value : null
  }
  return Number.isSafeInteger(entry.size) && entry.size! >= 0 ? entry.size! : null
}

export function fileList<T extends ListEntry>(source: readonly T[], query: string, kind: DisplayKind, sort: FileSort): T[] {
  const needle = query.trim().toLocaleLowerCase()
  const direction = sort === 'desc' || sort.endsWith('-desc') ? -1 : 1
  return source.filter(entry => (kind === 'all' || (kind === 'folders' ? entry.folder : !entry.folder)) && entry.name.toLocaleLowerCase().includes(needle)).sort((a,b) => {
    if (a.folder !== b.folder) return a.folder ? -1 : 1
    if (sort === 'asc' || sort === 'desc') return collator.compare(a.name,b.name) * direction
    if (a.folder) return collator.compare(a.name,b.name)
    if (sort.startsWith('size-')) {
      const left = displaySize(a), right = displaySize(b)
      if (left === null || right === null) {
        if (left !== right) return left === null ? 1 : -1
      } else if (left !== right) return (left - right) * direction
    } else {
      const type = collator.compare(fileExtension(a),fileExtension(b))
      if (type) return type * direction
    }
    return collator.compare(a.name,b.name)
  })
}
