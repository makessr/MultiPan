export type BrowseLocation = { id: string; breadcrumbs: {id:string; name:string}[] }
type Track = { entries: BrowseLocation[]; index: number }
const copy = (location: BrowseLocation): BrowseLocation => ({id:location.id,breadcrumbs:location.breadcrumbs.map(crumb=>({id:crumb.id,name:crumb.name}))})

// Successful locations only; no page contents, credentials, disk persistence,
// or network requests. Each account/source keeps at most 50 locations.
export class BrowseHistory {
  private tracks = new Map<string,Track>()

  current(scope: string): BrowseLocation | null {
    const track=this.tracks.get(scope),location=track?.entries[track.index]
    return location ? copy(location) : null
  }
  step(scope: string, delta: -1 | 1): {index:number;location:BrowseLocation} | null {
    const track=this.tracks.get(scope)
    if (!track) return null
    const index=track.index+delta,location=track.entries[index]
    return location ? {index,location:copy(location)} : null
  }
  record(scope: string, location: BrowseLocation) {
    if (!scope || !location.id) return
    let track=this.tracks.get(scope)
    if (!track) {track={entries:[],index:-1};this.tracks.set(scope,track)}
    if (track.entries[track.index]?.id===location.id) {
      track.entries[track.index]=copy(location)
      return // refresh must preserve the forward branch
    }
    track.entries=track.entries.slice(0,track.index+1)
    track.entries.push(copy(location))
    if (track.entries.length>50) track.entries.shift()
    track.index=track.entries.length-1
  }
  // Only call after the target was successfully loaded and still matches.
  commit(scope: string, index: number, location: BrowseLocation): boolean {
    const track=this.tracks.get(scope)
    if (!track || !Number.isInteger(index) || track.entries[index]?.id!==location.id) return false
    track.entries[index]=copy(location);track.index=index
    return true
  }
  clear(scope: string) { this.tracks.delete(scope) }
  retain(scopes: readonly string[]) {
    const wanted=new Set(scopes)
    for (const scope of this.tracks.keys()) if (!wanted.has(scope)) this.tracks.delete(scope)
  }
}
