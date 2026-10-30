-- What an uploaded file actually was, recorded at the moment it arrived.
--
-- All three columns are nullable, and NULL is a fact about the row rather than
-- a default: every attachment uploaded before this migration was never
-- inspected. "Not recorded" and "nothing wrong" are different statements, and
-- rendering the first as the second is a lie about a file nobody checked.
ALTER TABLE attachments
    -- What the content sniffs as, independent of what the filename claimed.
    -- Compared against the claimed extension to flag a mismatch; the mismatch
    -- is shown and never blocks, because legitimate ones exist (a .log holding
    -- a captured HTML response is an ordinary help desk attachment).
    --
    -- Note what this control is for, so it is not deleted as redundant:
    -- downloads are served as application/octet-stream with an attachment
    -- disposition and nothing renders, so a mismatch is not an XSS risk to us.
    -- It is a deception risk for the person about to open the file.
    ADD COLUMN detected_mime TEXT,

    -- SHA-256 of the bytes as uploaded, hex encoded: before image
    -- recompression and before quarantine wrapping. It answers "what did this
    -- person send us", which is the question an analyst looks up in VirusTotal
    -- and the one a chain-of-custody record has to answer. For a recompressed
    -- image it is therefore NOT the hash of the file on disk.
    ADD COLUMN sha256 TEXT,

    -- The scanner's name for the detection, e.g. Eicar-Test-Signature, for an
    -- upload that was found infected and quarantined. "Infected" alone tells
    -- an analyst nothing; the family name is the first thing they act on.
    --
    -- This is the one column here where NULL is a fact rather than an absence:
    -- it means the file was not identified as malicious.
    ADD COLUMN virus_name TEXT,

    -- Whether the content contradicted the name it was uploaded under.
    --
    -- Stored rather than derived, and that is the whole point of the column.
    -- It was first computed on read, from the stored filename against
    -- detected_mime — which is wrong for any file we renamed. A file wrapped
    -- as suspicious-<crc32>.zip, or an infected one stored as sample.pdf.zip,
    -- has lost the name the uploader claimed, so the comparison becomes ".zip"
    -- against the content, and a truthfully named infected file gets reported
    -- to staff as lying about itself. A warning that fires on honest files is
    -- one people learn to click past, which costs more than it saves.
    --
    -- Whether the content matched the name is a fact about the upload, known
    -- at the one moment both halves were in hand. It is recorded then.
    ADD COLUMN content_mismatch BOOLEAN;
