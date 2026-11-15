// How a grid cell shows a Postgres text value (and the class that colours it).

// undefined: a field the document (DynamoDB item, MongoDB document) hasn't got.
export function cellClass(value: string | null | undefined, category: string) {
  if (value === null) return 'cell-null'
  if (value === undefined) return 'cell-absent'
  if (category === 'bool') return value === 't' ? 'cell-bool-true' : 'cell-bool-false'
  return `cell-${category}`
}

// Text-format booleans arrive as "t" / "f".
export function cellText(value: string, category: string) {
  if (category === 'bool') return value === 't' ? 'true' : value === 'f' ? 'false' : value
  return value
}
