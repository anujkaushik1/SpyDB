// DynamoDB Local (AWS's DynamoDB for your own computer) for the end-to-end
// tests: a tiny client (it doesn't check signatures) and the made-up tables
// the tests use. Never AWS.

export const DDB = process.env.E2E_DDB ?? 'http://127.0.0.1:8000'

export async function ddb(op, body) {
  const r = await fetch(DDB, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/x-amz-json-1.0',
      'X-Amz-Target': `DynamoDB_20120810.${op}`,
      Authorization: 'AWS4-HMAC-SHA256 Credential=AKIADEMOEXAMPLE/20261115/ap-south-1/dynamodb/aws4_request, SignedHeaders=host, Signature=00',
    },
    body: JSON.stringify(body),
  })
  const out = await r.json()
  if (!r.ok) throw new Error(`${op}: ${JSON.stringify(out)}`)
  return out
}

export const getItem = async (table, key) => (await ddb('GetItem', { TableName: table, Key: key, ConsistentRead: true })).Item

// Every item of a table as one string per key ("u001|1731400001" → its
// fields' JSON, A to Z): comparing two snapshots shows exactly which items changed.
export async function snapshot(table, keyNames) {
  const out = {}
  let start
  do {
    const page = await ddb('Scan', { TableName: table, ConsistentRead: true, ...(start && { ExclusiveStartKey: start }) })
    for (const item of page.Items) {
      const key = keyNames.map((k) => Object.values(item[k])[0]).join('|')
      out[key] = Object.keys(item).sort().map((f) => `${f}=${JSON.stringify(item[f])}`).join(' ')
    }
    start = page.LastEvaluatedKey
  } while (start)
  return out
}

// Every item of a table, raw (the AttributeValue JSON), for working out what a filter should find.
export async function scanItems(table) {
  const out = []
  let start
  do {
    const page = await ddb('Scan', { TableName: table, ConsistentRead: true, ...(start && { ExclusiveStartKey: start }) })
    out.push(...page.Items)
    start = page.LastEvaluatedKey
  } while (start)
  return out
}

// Which keys differ between two snapshots (changed, added or removed), sorted.
export const changedKeys = (before, after) =>
  [...new Set([...Object.keys(before), ...Object.keys(after)])].filter((k) => before[k] !== after[k]).sort()

// orders: userId (text) + createdAt (number), 120 items of mixed types (a
// note on every 5th, a NULL on every 7th); events: 3 items keyed by id.
export async function seed() {
  for (const t of ['orders', 'events']) {
    try { await ddb('DeleteTable', { TableName: t }) } catch { /* not there yet */ }
  }
  await ddb('CreateTable', {
    TableName: 'orders', BillingMode: 'PAY_PER_REQUEST',
    AttributeDefinitions: [{ AttributeName: 'userId', AttributeType: 'S' }, { AttributeName: 'createdAt', AttributeType: 'N' },
      { AttributeName: 'city', AttributeType: 'S' }, { AttributeName: 'amount', AttributeType: 'N' }],
    KeySchema: [{ AttributeName: 'userId', KeyType: 'HASH' }, { AttributeName: 'createdAt', KeyType: 'RANGE' }],
    // An index like a real table's: by city, sorted by amount, keeping every field.
    GlobalSecondaryIndexes: [{ IndexName: 'cityIndex', KeySchema: [{ AttributeName: 'city', KeyType: 'HASH' }, { AttributeName: 'amount', KeyType: 'RANGE' }], Projection: { ProjectionType: 'ALL' } }],
  })
  await ddb('CreateTable', {
    TableName: 'events', BillingMode: 'PAY_PER_REQUEST',
    AttributeDefinitions: [{ AttributeName: 'id', AttributeType: 'S' }],
    KeySchema: [{ AttributeName: 'id', KeyType: 'HASH' }],
  })
  const cities = ['Delhi', 'Mumbai', 'Pune', 'Agra']
  for (let i = 0; i < 120; i++) {
    const item = {
      userId: { S: `u${String(i).padStart(3, '0')}` }, createdAt: { N: String(1731400000 + i) },
      city: { S: cities[i % 4] }, amount: { N: `${100 + i}.50` }, paid: { BOOL: i % 2 === 0 },
      tags: { SS: ['new', `t${i % 3}`] }, address: { M: { pin: { N: String(110001 + i) }, lines: { L: [{ S: 'line 1' }] } } },
    }
    if (i % 5 === 0) item.note = { S: `note ${i}` }
    if (i % 7 === 0) item.gone = { NULL: true }
    await ddb('PutItem', { TableName: 'orders', Item: item })
  }
  for (let i = 0; i < 3; i++) await ddb('PutItem', { TableName: 'events', Item: { id: { S: `e${i}` }, kind: { S: 'click' } } })
}
