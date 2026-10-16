-- Git push was removed: its columns go.
ALTER TABLE apps DROP COLUMN git_branch;
ALTER TABLE apps DROP COLUMN git_commit;
