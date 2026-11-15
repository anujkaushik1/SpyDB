// The MongoDB query editor's text: where each command is (for running the
// one at the cursor), and what to suggest while typing. No React or Wails
// imports, so the unit tests can load it.

export type Command = { from: number; to: number; text: string } // [from, to) in the text

// Splits the editor's text into commands: each starts with db at the start
// of a line (or after a ;), outside strings, comments and brackets, so a
// command can run over many lines.
export function splitCommands(text: string): Command[] {
  const starts: number[] = []
  let depth = 0
  let lineStart = true // only white space since the last line break or ;
  let i = 0
  while (i < text.length) {
    const c = text[i]
    if (c === '"' || c === "'" || c === '`') {
      lineStart = false
      i++
      while (i < text.length && text[i] !== c && text[i] !== '\n') i += text[i] === '\\' ? 2 : 1
      i++
    } else if (c === '/' && text[i + 1] === '/') {
      while (i < text.length && text[i] !== '\n') i++
    } else if (c === '/' && text[i + 1] === '*') {
      const end = text.indexOf('*/', i + 2)
      i = end < 0 ? text.length : end + 2
    } else {
      if ('([{'.includes(c)) depth++
      else if (')]}'.includes(c)) depth = Math.max(0, depth - 1)
      if (depth === 0 && lineStart && c === 'd' && /^db\b/.test(text.slice(i, i + 3))) starts.push(i)
      if (c === '\n' || (c === ';' && depth === 0)) lineStart = true
      else if (!/\s/.test(c)) lineStart = false
      i++
    }
  }
  return starts.map((from, k) => {
    const end = k + 1 < starts.length ? starts[k + 1] : text.length
    const raw = text.slice(from, end)
    const trimmed = raw.replace(/[\s;]+$/, '')
    return { from, to: from + trimmed.length, text: trimmed }
  })
}

// The command the cursor is in or just after; before the first, the first.
export function commandAt(text: string, cursor: number): Command | null {
  const commands = splitCommands(text)
  let pick: Command | null = commands[0] ?? null
  for (const c of commands) {
    if (c.from <= cursor) pick = c
    else break
  }
  return pick
}

// What Run runs: the selection; else the whole text when it's one command;
// else the command at the cursor.
export function commandToRun(text: string, from: number, to: number): Command | null {
  if (from !== to) return { from, to, text: text.slice(from, to) }
  const commands = splitCommands(text)
  if (commands.length <= 1) return text.trim() ? { from: 0, to: text.length, text } : null
  return commandAt(text, from)
}

export const METHODS = [
  'find', 'findOne', 'aggregate', 'countDocuments', 'estimatedDocumentCount', 'distinct',
  'insertOne', 'insertMany', 'updateOne', 'updateMany', 'replaceOne', 'deleteOne', 'deleteMany',
]
const CHAINED = ['sort', 'limit', 'skip', 'projection']
export const OPERATORS = [
  '$eq', '$ne', '$gt', '$gte', '$lt', '$lte', '$in', '$nin', '$exists', '$type', '$regex', '$options', '$and', '$or', '$nor', '$not',
  '$elemMatch', '$size', '$all', '$expr', '$set', '$unset', '$inc', '$push', '$pull', '$addToSet', '$rename', '$currentDate',
  '$match', '$group', '$project', '$sort', '$limit', '$skip', '$unwind', '$lookup', '$count', '$addFields', '$sum', '$avg', '$min', '$max', '$first', '$last',
]
const VALUES = ['ObjectId("")', 'ISODate("")', 'NumberLong()', 'NumberInt()', 'NumberDecimal("")', 'true', 'false', 'null']

export type Suggestion = { label: string; type: 'collection' | 'method' | 'operator' | 'field' | 'value' }

// Suggestions for the word before the cursor: collections after "db.",
// methods after "db.<collection>.", .sort() and the like after a call,
// operators after "$", else fields seen and values like ObjectId("…").
export function suggestions(before: string, collections: string[], fields: string[]): { from: number; options: Suggestion[] } | null {
  const word = /[\w$]*$/.exec(before)![0]
  const from = before.length - word.length
  const head = before.slice(0, from)
  if (/(^|[^\w$.])db\.$/.test(head)) return { from, options: collections.map((label) => ({ label, type: 'collection' })) }
  if (/(^|[^\w$.])db\.[\w$.]+\.$/.test(head) || /db\[['"][^'"]*['"]\]\.$/.test(head)) {
    return { from, options: METHODS.map((label) => ({ label, type: 'method' })) }
  }
  if (/\)\s*\.$/.test(head)) return { from, options: CHAINED.map((label) => ({ label, type: 'method' })) }
  if (word.startsWith('$')) return { from, options: OPERATORS.map((label) => ({ label, type: 'operator' })) }
  if (!word) return null
  return {
    from,
    options: [
      ...fields.map((label): Suggestion => ({ label, type: 'field' })),
      ...VALUES.map((label): Suggestion => ({ label, type: 'value' })),
    ],
  }
}

// How a collection is written in a command: db.orders, or
// db.getCollection("order-items") for a name that isn't a plain word.
export const collectionRef = (name: string) =>
  /^[A-Za-z_$][\w$]*$/.test(name) ? `db.${name}` : `db.getCollection(${JSON.stringify(name)})`
