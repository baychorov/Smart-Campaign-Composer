import type { Condition, Rule } from '../api/types'

// Rule edits made in the UI (decision: remove conditions or values, change
// numbers; no adding). Each edit returns a new rule that is sent with
// PUT /rule; the backend validates and normalises it and its answer
// replaces the local copy, so the UI never invents a rule of its own.

/**
 * Deep copy of a rule. A rule is plain JSON; structuredClone cannot copy the
 * reactive Proxy that Vue wraps props in, a JSON round trip can.
 */
function cloneRule(rule: Rule): Rule {
  return JSON.parse(JSON.stringify(rule)) as Rule
}

/** Where a condition lives: in common (group null) or in a group. */
export interface CondPath {
  group: number | null
  index: number
}

function conditionsAt(rule: Rule, path: CondPath): Condition[] {
  if (path.group === null) return rule.common
  const g = rule.groups[path.group]
  if (!g) throw new Error(`no group ${path.group}`)
  return g.conditions
}

/** Drops empty groups and fixes the group index of unsupported criteria. */
function dropEmptyGroups(rule: Rule): void {
  for (let g = rule.groups.length - 1; g >= 0; g--) {
    if (rule.groups[g]!.conditions.length > 0) continue
    rule.groups.splice(g, 1)
    for (const u of rule.unsupported) {
      if (u.group === g) u.group = null
      else if (u.group !== null && u.group > g) u.group--
    }
  }
}

export function removeCondition(rule: Rule, path: CondPath): Rule {
  const next = cloneRule(rule)
  conditionsAt(next, path).splice(path.index, 1)
  dropEmptyGroups(next)
  return next
}

/** Removes one city from an in/not_in list; an emptied list removes the condition. */
export function removeValue(rule: Rule, path: CondPath, valueIndex: number): Rule {
  const next = cloneRule(rule)
  const cond = conditionsAt(next, path)[path.index]
  if (!cond?.values) return next
  cond.values.splice(valueIndex, 1)
  if (cond.values.length === 0) return removeCondition(next, path)
  return next
}

/** The numbers the UI lets the marketer change. */
export type NumberField = 'value' | 'within_days' | 'min_count'

/**
 * Sets an age (value), an event window (within_days) or a count
 * (min_count). Without field, an event's window or an attribute's value.
 * Range checks are the backend's.
 */
export function setNumber(rule: Rule, path: CondPath, n: number, field?: NumberField): Rule {
  const next = cloneRule(rule)
  const cond = conditionsAt(next, path)[path.index]
  if (!cond) return next
  cond[field ?? (cond.type === 'event' ? 'within_days' : 'value')] = n
  return next
}

/** True when removing this condition would leave a filtered rule with no conditions. */
export function isLastCondition(rule: Rule): boolean {
  const total = rule.common.length + rule.groups.reduce((n, g) => n + g.conditions.length, 0)
  return rule.audience_scope === 'filtered' && total <= 1
}
