---
name: adr-split
description: Split an ADR or design document into implementation stories
---

Turn one architecture decision record or design document into kanban cards.

## Steps

1. Read the whole document before you propose anything. Note the decision it
   makes, the constraints it states, and the work it implies.
2. List the work items the document actually contains. A work item is
   independently shippable: it can be built, reviewed, and delivered without
   waiting for another item on the list. Split by deliverable, not by heading.
3. For each candidate, call `find_similar` with three to six words taken from
   its title. If a returned card already covers that work, drop the candidate.
   Never propose a card the board already has.
4. Call `propose_card` once per surviving item. One call per card. Do not put
   several stories in one call.
5. After the last card, reply with a short markdown summary: one line per
   proposed card saying what it covers, plus one line for anything you dropped
   as a duplicate or left out because the document only records a decision.

## Card fields

- `title`: imperative and specific. "Add rate limiting to the token endpoint",
  not "Rate limiting".
- `desc`: markdown. State the context the card needs from the document and why
  the work exists. A few sentences. Do not paste the document into it.
- `checks`: the acceptance criteria, one per entry, each one verifiable by
  someone reviewing the change. Two to five per card.
- `effort`: `S` for under a day, `M` for a few days, `L` for a week or more.
- `prio`: 1 is high, 2 is medium, and 3 is low. Use 3 unless the document says
  the item blocks other work.
- `tags`: single words, no spaces. Name the area the work touches, for example
  `backend`, `api`, `docs`.
- `emoji`: one emoji that suits the work, or leave it out.
- `due`: only when the document states a date. Format `YYYY-MM-DD`.

## Limits

Propose only as many stories as the document genuinely contains. Three real
items is a correct answer; five padded ones is not. Do not add tests,
documentation, or rollout cards the document does not ask for.

If `propose_card` returns an error, read it, fix that field, and call the tool
again for the same story.

If the document describes no implementable work, propose nothing and say so in
your reply.
