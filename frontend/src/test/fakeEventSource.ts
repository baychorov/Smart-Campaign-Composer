import type { EventSourceLike } from '../composables/useEventStream'

/** A scripted EventSource: tests push events and errors by hand. */
export class FakeEventSource implements EventSourceLike {
  static instances: FakeEventSource[] = []
  readyState = 0
  closed = false
  onopen: ((ev: Event) => unknown) | null = null
  onerror: ((ev: Event) => unknown) | null = null
  private listeners = new Map<string, ((ev: MessageEvent) => void)[]>()

  constructor(readonly url: string) {
    FakeEventSource.instances.push(this)
  }

  static create = (url: string) => new FakeEventSource(url)
  static last(): FakeEventSource {
    const es = FakeEventSource.instances[FakeEventSource.instances.length - 1]
    if (!es) throw new Error('no EventSource was created')
    return es
  }
  static reset() {
    FakeEventSource.instances = []
  }

  addEventListener(type: string, listener: (ev: MessageEvent) => void) {
    this.listeners.set(type, [...(this.listeners.get(type) ?? []), listener])
  }

  open() {
    this.readyState = 1
    this.onopen?.(new Event('open'))
  }

  emit(type: string, data: unknown) {
    const ev = new MessageEvent(type, { data: typeof data === 'string' ? data : JSON.stringify(data) })
    for (const l of this.listeners.get(type) ?? []) l(ev)
  }

  /** A dropped connection the browser retries (CONNECTING) or a refused one (CLOSED). */
  fail(readyState: 0 | 2) {
    this.readyState = readyState
    this.onerror?.(new Event('error'))
  }

  close() {
    this.closed = true
    this.readyState = 2
  }
}
