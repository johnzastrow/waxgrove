-- Re-sync needs to know whether the provider copy still looks the way we left
-- it. Spotify returns a snapshot_id that changes on every modification, so
-- storing the one we last wrote turns "has somebody edited this behind our
-- back?" into a comparison rather than a guess.
--
-- Without it, a re-sync either overwrites somebody's edits silently or refuses
-- every time — and D10 requires asking, not clobbering.
ALTER TABLE playlist_syncs ADD COLUMN provider_snapshot TEXT;

-- Forking (F20). A fork is a real playlist of the forker's own, with a pointer
-- back to where it came from.
--
-- ON DELETE SET NULL rather than CASCADE: if the original is deleted, the fork
-- is still the forker's playlist and must not vanish with it. It simply loses
-- the ability to say where it came from.
ALTER TABLE playlists ADD COLUMN forked_from TEXT REFERENCES playlists (id) ON DELETE SET NULL;
