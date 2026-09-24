ALTER TABLE attachments
    DROP COLUMN detected_mime,
    DROP COLUMN sha256,
    DROP COLUMN virus_name,
    DROP COLUMN content_mismatch;
