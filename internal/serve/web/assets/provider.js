// The look of a provider instance. The key of an instance ("aws", "aws.$west") picks one of twelve looks: one of the
// six hues of the providers' tints, plain or in the alternate. A type with no key falls back to its prefix ("aws" of
// aws_s3_bucket), which is the key of the default instance.
//
// A look comes from a hash of the key, so that it does not depend on the order in which the page meets the
// instances. setProviders then walks the keys of an estate in sorted order and moves a key that meets a look that is
// taken to a free one that is far from it, so that up to twelve instances differ.
const LOOKS = 12;
const HUES = 6;
let assigned = new Map();

const keyOf = (type, key) => key || (type || '').split('_')[0] || '?';

// FNV-1a with a final mix: short keys that differ in a letter or two land in different looks
function slotOf(k) {
  let hash = 2166136261;
  for (let i = 0; i < k.length; i++) hash = Math.imul(hash ^ k.charCodeAt(i), 16777619) >>> 0;
  hash = Math.imul(hash ^ (hash >>> 15), 2246822519) >>> 0;
  return ((hash ^ (hash >>> 13)) >>> 0) % LOOKS;
}

// setProviders gives each instance of the nodes its look, and of the project when it lists instances that no node uses
export function setProviders(nodes, project) {
  const named = ((project && project.providers) || []).flatMap((p) => (p.instances || []).map((i) => i.key));
  const keys = [...new Set([...(nodes || []).map((n) => keyOf(n.type, n.provider)), ...named])].sort();
  const taken = new Set();
  const next = new Map();
  for (const k of keys) {
    const home = slotOf(k);
    let slot = home;
    // the free looks in an order that moves three hues on first, then to the other lightness, then one hue on
    for (let j = 0; j < LOOKS && taken.has(slot) && taken.size < LOOKS; j++) slot = (home + (j % 4) * 3 + Math.floor(j / 4) + 3) % LOOKS;
    taken.add(slot);
    next.set(k, slot);
  }
  assigned = next;
}

// providerOf returns the monogram and the look of the provider of a resource: the instance key when there is one
export function providerOf(type, key) {
  const k = keyOf(type, key);
  const slot = assigned.has(k) ? assigned.get(k) : slotOf(k);
  const base = k.split('.')[0];
  return { hue: slot % HUES, alt: slot >= HUES, mono: (base.length <= 3 ? base : base.slice(0, 2)).toUpperCase() };
}

// pvClass is the class that carries the look: pv0 to pv5, and pvx for the alternate look
export const pvClass = (p) => 'pv' + p.hue + (p.alt ? ' pvx' : '');
