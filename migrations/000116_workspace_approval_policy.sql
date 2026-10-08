-- Existing and newly created workspaces keep independent approval by default.
ALTER TABLE tenants
 ADD COLUMN allow_agent_plan_self_approval BOOLEAN NOT NULL DEFAULT FALSE,
 ADD COLUMN allow_rule_self_approval BOOLEAN NOT NULL DEFAULT FALSE,
 ADD COLUMN approval_policy_revision INTEGER NOT NULL DEFAULT 1 CHECK (approval_policy_revision > 0),
 ADD COLUMN approval_policy_updated_by TEXT NOT NULL DEFAULT '',
 ADD COLUMN approval_policy_updated_at TIMESTAMPTZ;
