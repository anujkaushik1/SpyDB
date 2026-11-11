import assert from 'node:assert/strict'
import { test } from 'node:test'
import { setupFraction, setupText } from '../src/dynamo/setupText'

test('downloading says why and how much', () => {
  const s = { stage: 'downloading', done: 23_400_000, total: 61_141_180, older: '2.27.6' }
  assert.equal(setupText(s), 'Downloading the AWS CLI (your AWS CLI 2.27.6 is too old for this): 23 of 61 MB')
  assert.equal(setupFraction(s)?.toFixed(2), '0.38')
})

test('with no CLI at all, and before the size is known', () => {
  const s = { stage: 'downloading', done: 0, total: -1, older: '' }
  assert.equal(setupText(s), 'Downloading the AWS CLI (this Mac has no AWS CLI)…')
  assert.equal(setupFraction(s), null)
})

test('installing and done', () => {
  assert.equal(setupText({ stage: 'installing', done: 0, total: 0, older: '' }), 'Installing the AWS CLI in SpyDB’s folder (one time only)…')
  assert.equal(setupText({ stage: 'done', done: 0, total: 0, older: '' }), 'AWS CLI installed.')
  assert.equal(setupFraction({ stage: 'installing', done: 0, total: 0, older: '' }), null)
})
