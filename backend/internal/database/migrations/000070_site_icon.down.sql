-- Drops the uploaded icon with its tables; it cannot be brought back except
-- from a backup. The site_short_name setting row is left alone, as 000052 and
-- 000067 leave theirs: it is an administrator's choice, and older code never
-- reads it.
DROP TABLE site_icon_files;
DROP TABLE site_icon;
