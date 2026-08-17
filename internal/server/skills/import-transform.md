---
name: import-transform
description: Turn numbered forge issues into kanban card proposals
---

The input is a list of forge issues. Each one starts with a line `Source N`,
where N is its number, and carries a title, a ref, labels, a body, and
comments.

## Steps

1. Read every issue.
2. Decide per issue whether it is real work. Skip pure noise: a duplicate
   report, a question already answered, a "+1" thread, an empty issue.
3. Call `propose_card` once for every issue that survives. One call per issue.
   Never merge two issues into one card, and never split one issue into two.
4. Set `source` to the `Source N` number of the issue that call came from. Use
   only a number that appears in the input. Never guess one, never count your
   own calls, and never leave it out.
5. After the last call, reply with one or two sentences: how many cards you
   proposed and what you skipped. No JSON, no card bodies.

## Card fields

- `title`: imperative and specific. Rewrite the issue title into the work to
  be done; do not copy it verbatim.
- `emoji`: exactly one emoji character that suits the work, or an empty string.
- `desc`: markdown. State the problem and why it matters, in your own words.
  Do not paste the issue body or its comments into it.
- `prio`: an integer, 1 is highest and 4 is lowest. Use 3 unless the issue is
  explicitly urgent or blocking.
- `due`: a date as `YYYY-MM-DD`, or an empty string. Only when the issue states
  a date.
- `effort`: `S` for under a day, `M` for a few days, `L` for a week or more.
- `tags`: single words, no spaces, no leading `#`, taken from the issue labels
  or the area it touches. Never add a `link::` tag or any other tag that
  encodes a URL or a ref; the server records where a card came from.
- `checks`: acceptance criteria, one per entry, two to five of them.

If `propose_card` returns an error, read it, fix that field, and call the tool
again for the same issue.

If no issue merits a card, propose nothing and say so.
