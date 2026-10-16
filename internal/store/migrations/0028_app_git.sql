-- An app deployed by git push to the SSH gateway: the branch it deploys ('' for an app of another source)
-- and the commit it runs ('' before the first push), built on its node from the repository the server keeps.
ALTER TABLE apps ADD COLUMN git_branch TEXT NOT NULL DEFAULT '';
ALTER TABLE apps ADD COLUMN git_commit TEXT NOT NULL DEFAULT '';
