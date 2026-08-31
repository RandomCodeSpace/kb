---
name: story-draft
description: Draft one kanban card from a request, or rewrite an existing card
---

Turn the request into exactly one kanban card.

## Steps

1. Read the request. If it ends with a `Current card JSON:` block, that block is
   the card being updated: start from it, apply what the request asks for, and
   keep every field the request does not change.
2. You may call `find_similar` once first. A match never cancels the card: this
   request owes the user exactly one proposal, and whether to keep a card that
   overlaps an existing one is the user's decision, not yours. Name the match in
   your closing sentence instead.
3. Call `propose_card` exactly once. Always call it, whatever `find_similar`
   returned. Do not call it a second time.
4. When the request is an update, the call must carry the whole updated card,
   not only the changed fields.
5. Reply with one short sentence, or nothing at all. Never write the card as
   JSON or markdown in your reply.

## Card fields

- `title`: imperative and specific. "Add rate limiting to the token endpoint",
  not "Rate limiting".
- `emoji`: exactly one emoji character that suits the work, or an empty string
  when nothing fits.
- `desc`: markdown. State what the work is and why it exists. A few sentences.
- `prio`: an integer: 1 is high, 2 is medium, and 3 is low. Use 3 unless the
  request says otherwise.
- `due`: a date as `YYYY-MM-DD`, or an empty string. Only when the request
  states a date.
- `effort`: `S` for under a day, `M` for a few days, `L` for a week or more, or
  an empty string.
- `tags`: single words, no spaces, no leading `#`. Name the area the work
  touches, for example `backend`, `api`, `docs`.
- `checks`: acceptance criteria, one per entry, each verifiable by whoever
  reviews the change. Two to five of them.

If `propose_card` returns an error, read it, fix that field, and call the tool
again for the same card. That retry is the only extra `propose_card` call
allowed.
