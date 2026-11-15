// Fuzzy matching for pickers: "crat" finds created_at, "uid" finds user_id.
// Kept free of React so tests can load it.

export type FuzzyMatch = { score: number; positions: number[] } // positions: the matched characters, for highlighting

// How well `query` matches `text`, or null if it doesn't. Lower scores are
// better: an exact name first, then a prefix, then a match at word starts
// (after _ . - or a capital), then a run of letters anywhere, then letters
// in order with gaps. Case doesn't matter.
export function fuzzyMatch(query: string, text: string): FuzzyMatch | null {
  const q = query.trim().toLowerCase()
  if (!q) return { score: 0, positions: [] }
  const t = text.toLowerCase()
  if (t === q) return { score: 0, positions: range(0, q.length) }
  if (t.startsWith(q)) return { score: 1, positions: range(0, q.length) }
  // The query as a run of letters: at a word start if it is anywhere, else the first place.
  let at = t.indexOf(q)
  for (let i = at; i >= 0; i = t.indexOf(q, i + 1)) {
    if (isWordStart(text, i)) { at = i; break }
  }
  if (at >= 0) return { score: isWordStart(text, at) ? 2 : 3, positions: range(at, q.length) }
  // Letters in order: prefer ones at word starts, and short gaps.
  const positions: number[] = []
  let from = 0
  let gaps = 0
  let starts = 0
  for (const ch of q) {
    let i = t.indexOf(ch, from)
    if (i < 0) return null
    // A word-start occurrence a little further on beats this one.
    for (let j = i; j >= 0 && j < i + 12; j = t.indexOf(ch, j + 1)) {
      if (isWordStart(text, j)) { i = j; break }
      if (j < 0) break
    }
    if (isWordStart(text, i)) starts++
    if (positions.length > 0) gaps += i - positions[positions.length - 1] - 1
    positions.push(i)
    from = i + 1
  }
  return { score: 4 + Math.min(gaps, 20) / 21 - starts / (q.length * 10), positions }
}

// Ranks items by how well `query` matches their text; items that don't
// match are left out. Ties keep the given order.
export function fuzzyRank<T>(query: string, items: T[], text: (item: T) => string): { item: T; match: FuzzyMatch }[] {
  const out: { item: T; match: FuzzyMatch; i: number }[] = []
  items.forEach((item, i) => {
    const match = fuzzyMatch(query, text(item))
    if (match) out.push({ item, match, i })
  })
  return out.sort((a, b) => a.match.score - b.match.score || a.i - b.i).map(({ item, match }) => ({ item, match }))
}

function isWordStart(text: string, i: number): boolean {
  if (i === 0) return true
  const prev = text[i - 1]
  if ('_-. /'.includes(prev)) return true
  const ch = text[i]
  return ch !== ch.toLowerCase() && prev === prev.toLowerCase() // camelCase: the capital after a lower-case letter
}

const range = (from: number, n: number) => Array.from({ length: n }, (_, i) => from + i)
