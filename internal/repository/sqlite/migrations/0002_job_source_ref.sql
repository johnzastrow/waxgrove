-- An import job needs to carry the thing the user pasted, and it has to
-- survive a restart — otherwise a job requeued after a reboot has no idea what
-- it was importing.
--
-- It gets its own column rather than borrowing `error`, which is user-facing
-- text and would collide the moment the job actually failed.
ALTER TABLE jobs ADD COLUMN source_ref TEXT;
