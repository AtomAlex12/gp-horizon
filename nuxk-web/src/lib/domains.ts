// Glued domains: two lines of a list that lost the newline between them
// ("1e100.net" + "ytimg.com" → "1e100.netytimg.com"). Such an entry is a valid
// name that matches neither site, so nothing flags it — this does.

// TLDs a glued line most often ends with; a label that starts with one of
// these (and continues) is where two domains may have been joined.
const TLDS = [
  'com', 'net', 'org', 'info', 'biz', 'pro', 'online', 'site', 'club', 'top', 'xyz', 'space', 'tech', 'world',
  'media', 'party', 'land', 'photos', 'ovh', 'lol', 'app', 'dev', 'io', 'ai', 'me', 'co', 'cc', 'tv', 'to', 'ws',
  'gg', 'su', 'ru', 'ua', 'by', 'kz', 'de', 'eu', 'uk', 'us', 'jp', 'nl', 'fr', 'pl', 'is', 'my', 'fm', 'ly',
].sort((a, b) => b.length - a.length); // "online" before "on…" prefixes

/** Registrable-name label: "proton" for "account.proton.me". */
const sld = (d: string) => {
  const l = d.split('.');
  return l.length >= 2 ? l[l.length - 2] : '';
};

/** d, or one of its parent domains (2+ labels), is in known. */
function covered(d: string, known: Set<string>): boolean {
  const l = d.split('.');
  for (let i = 0; i + 2 <= l.length; i++) if (known.has(l.slice(i).join('.'))) return true;
  return false;
}

/** Every way to cut d into "a.b.tld" + "rest.tld" at a label starting with a TLD. */
function cuts(d: string): [string, string][] {
  const labels = d.split('.');
  const out: [string, string][] = [];
  for (let i = 1; i < labels.length - 1; i++) {
    const l = labels[i];
    if (TLDS.includes(l)) continue; // "chat.openai.com.cdn.cloudflare.net": "com" is whole, not "co"+"m"
    for (const tld of TLDS) {
      if (l.length <= tld.length || !l.startsWith(tld)) continue;
      const head = l.slice(tld.length);
      if (!/^[a-z0-9]/.test(head)) continue;
      out.push([[...labels.slice(0, i), tld].join('.'), [head, ...labels.slice(i + 1)].join('.')]);
    }
  }
  return out;
}

/** Cut a glued name all the way down: "a.comb.netc.org" → a.com, b.net, c.org. */
function explode(d: string): string[] {
  const c = cuts(d)[0];
  return c ? [...explode(c[0]), ...explode(c[1])] : [d];
}

export type Glued = { domain: string; parts: string[] };

/**
 * Entries of `domains` that look like two or more domains glued together.
 * A cut counts only with evidence from the lists themselves (`known` — every
 * domain the user has): one side is listed (or under a listed domain), or its
 * name matches a listed one — so "api.companyname.com" is left alone.
 */
export function findGlued(domains: string[], known: Iterable<string> = domains): Glued[] {
  const all = new Set(known);
  const names = new Map<string, number>();
  for (const k of all) names.set(sld(k), (names.get(sld(k)) ?? 0) + 1);
  const out: Glued[] = [];
  for (const d of domains) {
    // a part's name must be listed by some entry other than d itself
    const named = (x: string) => (names.get(sld(x)) ?? 0) > (sld(x) === sld(d) && all.has(d) ? 1 : 0);
    for (const [a, b] of cuts(d)) {
      // (a part is shorter than d, so d is never among its parents)
      if (covered(a, all) || covered(b, all) || named(a) || named(b)) {
        out.push({ domain: d, parts: [...explode(a), ...explode(b)] });
        break;
      }
    }
  }
  return out;
}
