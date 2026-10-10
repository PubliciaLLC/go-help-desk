# Phone layout: notes for #414, #425 and #426

Status: working notes, no code. Written so whoever picks these up starts from what was measured instead of re-deriving it. Facts are from `origin/v1.3.0-beta` (app) and `docs/web-1-3-0` (website, PR #430); line numbers will drift.

All three were found while taking the 1.3.0 website screenshots at 390 x 844 (the viewport DESIGN.md "Small screens" and `frontend/e2e/mobile-layout.spec.ts` pin). They are cosmetic: nothing is unreachable. They matter because they show on the marketing page's phone shots, and #414 and #426 sit in the two jobs DESIGN.md says a phone is for (a staff member triaging, a reporter following a request).

| Issue | Where | Fix lands on |
|---|---|---|
| #414 ticket list search squeezed, tracking number breaks mid-token | `frontend/src/pages/TicketListPage.tsx`, `TicketDetailPage.tsx` | app branch (`v1.3.0-beta`) |
| #426 description wraps with `break-all` | `frontend/src/pages/TicketDetailPage.tsx` | app branch |
| #425 website scrolls sideways on a phone | `style.css` and some page content | website branch (`v1.3.0-beta-web`) |

They are independent; the first two are one small change in two files, the third is a different repository branch and a different review.

## #414 (a): the search box is squeezed to "Searc"

**What happens.** At 390 px the ticket list toolbar puts the search input, the "Include closed" checkbox and the "Jump to ticket" button on one line. The search input shrinks until only "Searc" of its icon and placeholder shows.

**Why.** `TicketListPage.tsx` around line 202: the toolbar is a flex row with wrapping enabled, and the search box's wrapper takes the remaining space (`flex-1`, with a maximum width). A flex item with `flex: 1` has a base size of zero, so as far as the wrapping algorithm is concerned it always "fits": the line never wraps, the other two items (one of which is explicitly non-wrapping) keep their width, and the search box gets whatever is left, which at phone width is almost nothing. Wrapping was meant to be the safety valve and it never triggers for that item.

**Direction (no code).** Below the `md` breakpoint the search box should take a full row of its own, with the scope buttons (admin only), the closed toggle and the jump button on the row(s) below. Above `md` nothing should change. Give the search wrapper a minimum usable width, or make it span the whole row on small screens; either makes the wrapping algorithm do its job. Check the admin-only scope control (Mine / Unassigned / All): with it present the toolbar has four items, and it also must not squeeze.

**Do not** hide the jump button or the toggle on phones: DESIGN.md says staff triage is a phone job.

## #414 (b): the tracking number breaks inside itself

**What happens.** On the ticket detail header the line "GHD-2026-000001 · Opened ..." wraps inside the tracking number ("GHD-2026-" on one line, "000001" on the next).

**Why.** `TicketDetailPage.tsx` around line 513: the number and the "Opened ..." text are two spans in a flex row that does not wrap and lets both shrink. The hyphens in the number are legal line-break points, so when the row runs out of width the number is broken at a hyphen.

**Direction.** The tracking number is an identifier staff read aloud and search for, so it should never be split. Keep the number on one line (do not let it shrink or break) and let the "Opened ..." part move to its own line when the row is too narrow. Avoid replacing the hyphens with non-breaking hyphen characters: they change what is selected and copied, and what a text search for the number matches. Check the same pattern anywhere else a tracking number is printed next to other text (the ticket list cards, the follow-up confirmation, the guest ticket page).

## #426: `break-all` splits ordinary words

**What happens.** The description on the ticket detail page splits words anywhere ("la ptop", "ne ed", "panel i s"), most visibly at phone width.

**Why.** `TicketDetailPage.tsx` line 624: the description paragraph uses `break-all`, which breaks at any character. The reply bodies two screens below (line 658) already use `break-words`, which breaks inside a word only when the word cannot fit on a line at all. So this is an inconsistency, not a design choice. The guest ticket page (`GuestTicketViewPage.tsx`, lines 130 and 151) uses neither, and relies on the browser default.

**Direction.** Use the same wrapping rule as the replies. The reason someone chose `break-all` is almost certainly a very long unbroken token (a URL, a hash, a pasted log line) stretching the page; `break-words` still protects against that. Also check which other places legitimately want `break-all` and leave them alone: attachment filenames, SHA-256 values and threat names in `AttachmentList.tsx`, API key and OAuth secret displays, webhook URLs. Those are identifiers, not prose.

## #425: the website scrolls sideways on a phone

**What happens.** On the unchanged website base, pages overflow at 375 px: measured 589 px on the front page, 897 px on `docs/api.html` and 568 px on `docs/mcp.html`. PR #430 does not fix it, and a few pages got wider because of new wide code blocks (getting-started 630 to 820 px, the 1.2.0 upgrade page 500 to 685 px).

**Causes found.**
1. The docs layout is a two-column grid (a 240 px sidebar and `1fr`). A `1fr` column cannot shrink below its content's minimum width, so one wide table or code block stretches the whole page. Below 768 px the grid is already switched to a single column, but that single `1fr` column still takes its content's minimum width. Letting the column and the content box shrink below their content's width (the standard `minmax(0, ...)` and `min-width: 0` remedy) took `api.html` from 500 px to 443 px in a test.
2. The top navigation links are wider than the viewport, so every page still scrolls to about 443 px. The navigation needs its own small-screen layout (wrap, or collapse behind a menu button). This is the larger design decision of the three: the site has no menu button today.
3. Code blocks and tables should scroll inside their own box instead of widening the page. Code blocks already have a horizontal scroll rule; tables do not.

**Also seen.** The site sets smooth scrolling globally, which cuts short the scroll correction in the Redoc-based API reference (that page works around it locally). A site-wide fix is optional.

**Verification.** Check every page at 375 px: there is no automated check on the site, so the cheapest guard is a small script (outside the page build) that loads each page at 375 px in headless Chromium and fails when the document is wider than the viewport. The reviewers of #430 wrote throwaway versions of exactly that check.

## Tests

- **App (#414, #426):** `frontend/e2e/mobile-layout.spec.ts` only checks that `<main>` does not scroll sideways at 390 px, and that is why none of these were caught. Add assertions that name the defect: the search input's rendered width is above a usable minimum on the ticket list at 390 px; the tracking number's bounding box sits on a single line on the ticket detail page; a description containing a long unbroken URL still does not overflow, while an ordinary sentence is not split mid-word (compare the text's line boxes with the words). These need the long-text fixture the e2e mock does not have yet.
- **Unit:** nothing sensible; this is layout, and jsdom does not lay anything out.
- **Website (#425):** the headless width check above, run by hand before the deploy.

## Risks and things to leave alone

- Do not touch the admin pages: DESIGN.md says administration is not a phone job.
- The desktop table is a separate DOM branch from the phone cards (both are in the DOM, CSS picks one). A change to one must be checked against the other.
- #422 (audit diffs show raw ids) and the other findings in the screenshot reviews are separate issues, not part of this.

## Open questions

1. Should the search box also stay one line on a *tablet* width (768 px and up)? The current rule would keep one line from `md`.
2. For the website navigation (#425), is a menu button acceptable (it needs a few lines of script on a site that has none), or should the links wrap onto a second row?
3. Does the guest ticket page, which uses neither `break-all` nor `break-words`, need the same wrapping rule as the signed-in page?
