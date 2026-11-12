import assert from 'node:assert/strict'
import { test } from 'node:test'
import { activeAfterClose, tabsToClose } from '../src/tabActions'

const tabs = ['a', 'b', 'c', 'd']

test('which tabs each command closes', () => {
  assert.deepEqual(tabsToClose(tabs, 'b', 'close'), ['b'])
  assert.deepEqual(tabsToClose(tabs, 'b', 'others'), ['a', 'c', 'd'])
  assert.deepEqual(tabsToClose(tabs, 'b', 'right'), ['c', 'd'])
  assert.deepEqual(tabsToClose(tabs, 'd', 'right'), [], 'the last tab has none to its right')
  assert.deepEqual(tabsToClose(tabs, 'b', 'all'), tabs)
  assert.deepEqual(tabsToClose(tabs, 'x', 'all'), [], 'a tab that is gone closes nothing')
})

test('the active tab afterwards', () => {
  assert.equal(activeAfterClose(tabs, ['b'], 'c'), 'c', 'another tab closed: unchanged')
  assert.equal(activeAfterClose(tabs, ['c'], 'c'), 'b', 'the active one closed: its left neighbour')
  assert.equal(activeAfterClose(tabs, ['a'], 'a'), 'b', 'the first one closed: its right neighbour')
  assert.equal(activeAfterClose(tabs, ['a', 'c', 'd'], 'd', 'b'), 'b', 'Close Others: the tab it was used on')
  assert.equal(activeAfterClose(tabs, ['c', 'd'], 'd', 'b'), 'b', 'Close Tabs to Right: the tab it was used on')
  assert.equal(activeAfterClose(tabs, ['c', 'd'], 'a', 'b'), 'a', 'the active one stays: unchanged')
  assert.equal(activeAfterClose(tabs, tabs, 'b', 'b'), '', 'Close All: none')
})
