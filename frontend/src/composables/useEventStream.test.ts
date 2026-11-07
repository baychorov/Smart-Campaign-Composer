import { afterEach, describe, expect, it, vi } from 'vitest'
import { effectScope } from 'vue'
import { FakeEventSource } from '../test/fakeEventSource'
import { useEventStream } from './useEventStream'

afterEach(() => FakeEventSource.reset())

function setup() {
  const onEvent = vi.fn()
  const scope = effectScope()
  const s = scope.run(() =>
    useEventStream({ events: ['delta', 'done'], terminal: ['done'], onEvent, createSource: FakeEventSource.create }),
  )!
  return { s, onEvent, scope }
}

describe('useEventStream', () => {
  it('parses events and closes after a terminal one', () => {
    const { s, onEvent } = setup()
    s.connect('/x')
    const es = FakeEventSource.last()
    es.open()
    expect(s.state.value).toBe('open')
    es.emit('delta', { text: 'a' })
    es.emit('done', {})
    expect(onEvent.mock.calls).toEqual([
      ['delta', { text: 'a' }],
      ['done', {}],
    ])
    // Without close() EventSource would reconnect to the finished stream.
    expect(es.closed).toBe(true)
    expect(s.state.value).toBe('closed')
  })

  it('waits while the browser retries a dropped connection', () => {
    const { s } = setup()
    s.connect('/x')
    const es = FakeEventSource.last()
    es.open()
    es.fail(0)
    expect(s.state.value).toBe('connecting')
    expect(es.closed).toBe(false)
  })

  it('reports a refused connection as an error', () => {
    const { s } = setup()
    s.connect('/x')
    FakeEventSource.last().fail(2)
    expect(s.state.value).toBe('error')
  })

  it('ignores events of a replaced connection', () => {
    const { s, onEvent } = setup()
    s.connect('/old')
    const old = FakeEventSource.last()
    s.connect('/new')
    expect(old.closed).toBe(true)
    old.emit('delta', { text: 'late' })
    expect(onEvent).not.toHaveBeenCalled()
  })

  it('closes when its component scope ends', () => {
    const { s, scope } = setup()
    s.connect('/x')
    scope.stop()
    expect(FakeEventSource.last().closed).toBe(true)
  })
})
