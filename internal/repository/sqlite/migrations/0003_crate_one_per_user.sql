-- One crate per user. The crate is implicit — a user should never have to
-- create one before staging a song — which means it is created on first use,
-- which means two concurrent requests can race to create it.
--
-- Without this index the ON CONFLICT that resolves that race has nothing to
-- conflict on, and the loser silently creates a second crate: the user's
-- staged songs would then appear and disappear depending on which row was
-- read.
CREATE UNIQUE INDEX ux_crates_user ON crates (user_id);
