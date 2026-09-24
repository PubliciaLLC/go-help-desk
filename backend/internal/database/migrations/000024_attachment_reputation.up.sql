-- What a reputation provider said about a file, keyed by the file's hash.
--
-- Against the hash and not the attachment id, deliberately: the same file
-- uploaded to five tickets is one lookup, not five. That is what makes a free
-- tier of 500 lookups a day comfortable on an instance that sees the same
-- handful of samples repeatedly.
--
-- Primary key (sha256, provider), because a verdict is a statement by one
-- provider and not a property of the file. An operator who switches from
-- VirusTotal to MetaDefender must not be shown the other service's answer
-- attributed to the one they chose; two rows per hash is the shape that makes
-- that impossible rather than merely unlikely.
CREATE TABLE attachment_reputation (
    -- SHA-256 of the bytes as uploaded, hex encoded — the same value stored in
    -- attachments.sha256 by 000023. No foreign key: the verdict is about a
    -- file, and it stays true and useful after every attachment carrying that
    -- hash has been deleted.
    sha256      TEXT        NOT NULL,

    -- Which service said it: "virustotal", "metadefender", "polyswarm" or
    -- "circl". Stored rather than assumed from the current setting, because
    -- the setting can change after the row is written and the row would then
    -- be mislabelled.
    --
    -- Constrained, because the column and internal/reputation's provider
    -- constants have to agree on the spelling: a row written under a
    -- misspelled provider is a cached verdict nothing can ever read back, and
    -- every page render then spends a fresh lookup to rediscover it.
    provider    TEXT        NOT NULL
        CHECK (provider IN ('virustotal', 'metadefender', 'polyswarm', 'circl')),

    -- unseen | unscanned | clean | detected | known | unavailable.
    --
    -- NOT NULL, and every row records one of the six. "unavailable" is a
    -- state and not an absence: a lookup that failed, timed out or was
    -- rate-limited must never come back as clean. This is the same mistake
    -- the ClamAV scanner has now had fixed twice, and storing it as a real
    -- state is what keeps it from being reintroduced at the read side.
    --
    -- CHECKed rather than merely documented. The value this refuses is the
    -- empty string: a zero-valued reputation.Reputation has State "", nothing
    -- downstream has a branch for it, and it falls through to whatever the
    -- no-problem-here arm renders. A Go-side check would work too, but the
    -- database is the last place the value can be wrong, and that is where it
    -- should be refused.
    state       TEXT        NOT NULL
        CHECK (state IN ('unseen', 'unscanned', 'clean', 'detected', 'known', 'unavailable')),

    -- How many engines flagged it, and how many ran.
    --
    -- Both nullable, and NULL is a fact rather than a default: only a
    -- completed analysis has counts. For unseen, unscanned, known and
    -- unavailable the provider gave us no numbers at all, and 0 of 0 would
    -- render as "no engine found anything" — which is precisely the false
    -- reassurance the whole feature exists to avoid. NULL means "not
    -- recorded". known is the one worth spelling out: a file answered out of a
    -- catalogue is never scanned at all, so a count there would be a
    -- fabricated analysis attached to the one verdict staff are entitled to
    -- find reassuring.
    detected    INTEGER,
    total       INTEGER,

    -- The feeds that carry a "known" file: "nsrl", "microsoft_windows", a
    -- commercial software catalogue. Empty on every other state.
    --
    -- Carried because the names ARE the evidence, and because the state on its
    -- own deliberately does not say how strong the claim is. An Authenticode
    -- signature assertion and an NSRL catalogue entry are different things —
    -- NSRL catalogues hacking tools — and "known" is the one verdict in this
    -- feature that renders as reassurance, so the claim has to have a source
    -- attached, like every other claim here does.
    --
    -- NOT NULL DEFAULT '{}' rather than nullable, unlike the counts above. The
    -- reason the counts are nullable does not apply: an empty list cannot be
    -- misread as a finding the way 0 of 0 can. An empty array says nobody has
    -- this file catalogued, which is exactly true of every row that is not
    -- "known".
    known_feeds TEXT[] NOT NULL DEFAULT '{}',

    -- The provider's name for what it found, e.g. Trojan.GenericKD.12345.
    -- NULL when nothing was detected or no analysis exists.
    --
    -- Attacker-influenced text: a malware author picks the filename and
    -- therefore influences what the engines call it. Escaped like any other
    -- untrusted string on the way to a browser.
    threat_name TEXT,

    -- When the PROVIDER last analysed the file, not when we asked. NULL when
    -- they never have (unseen), or told us nothing about it (unscanned,
    -- unavailable). Staff read this to judge whether a clean verdict predates
    -- the sample's first appearance in the wild.
    analysed_at TIMESTAMPTZ,

    -- When WE fetched it, as distinct from analysed_at, which is when the
    -- provider analysed. NOT NULL, because we always know this: it is our own
    -- clock and there is no row without a lookup behind it.
    --
    -- Both expiry rules read this column. A verdict older than the configured
    -- refresh interval is re-fetched instead of returned, except a detection,
    -- which never expires — engines do not un-flag a file. And a staff member
    -- can force a re-check once per hash per seven days whatever that setting
    -- says, measured from here.
    --
    -- Which is why a re-check that comes back with the same answer must still
    -- re-stamp this: otherwise the control re-arms immediately and the next
    -- reader spends a lookup learning nothing.
    fetched_at  TIMESTAMPTZ NOT NULL DEFAULT now(),

    PRIMARY KEY (sha256, provider)
);
