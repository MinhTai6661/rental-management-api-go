-- +goose Up
-- The audit trail: what people and the system did, kept apart in two tables of the same shape. audit_logs holds what
-- matters for years (money, rights, contracts, roles, settings), audit_activity the daily noise (sign-ins) that is
-- dropped after three months. Both only ever grow, except for erasing the address and device of a removed account.
-- There are no foreign keys on purpose: the trail must outlive the rows it talks about. Postgres cuts each table into
-- one partition per month, so old months are dropped whole instead of deleted row by row.
CREATE TABLE audit_logs (
    id             bigserial,
    created_at     timestamptz NOT NULL DEFAULT now(),
    actor_id       uuid,
    -- The role the actor acted in; 'system' for jobs and commands.
    actor_role     varchar(20),
    -- The landlord who owns the data when somebody else acted on it.
    acting_for_id  uuid,
    action         varchar(60) NOT NULL,
    room_id        uuid,
    contract_id    uuid,
    invoice_id     uuid,
    payment_id     uuid,
    target_user_id uuid,
    changes        jsonb,
    meta           jsonb,
    ip             varchar(45),
    user_agent     varchar(120),
    session_id     uuid,
    request_id     varchar(64),
    PRIMARY KEY (id, created_at)
) PARTITION BY RANGE (created_at);

CREATE TABLE audit_activity (
    id             bigserial,
    created_at     timestamptz NOT NULL DEFAULT now(),
    actor_id       uuid,
    actor_role     varchar(20),
    acting_for_id  uuid,
    action         varchar(60) NOT NULL,
    room_id        uuid,
    contract_id    uuid,
    invoice_id     uuid,
    payment_id     uuid,
    target_user_id uuid,
    changes        jsonb,
    meta           jsonb,
    ip             varchar(45),
    user_agent     varchar(120),
    session_id     uuid,
    request_id     varchar(64),
    PRIMARY KEY (id, created_at)
) PARTITION BY RANGE (created_at);

-- One partial index per reference column: most rows carry only some of them.
CREATE INDEX audit_logs_actor_idx ON audit_logs (actor_id, created_at DESC) WHERE actor_id IS NOT NULL;
CREATE INDEX audit_logs_acting_for_idx ON audit_logs (acting_for_id, created_at DESC) WHERE acting_for_id IS NOT NULL;
CREATE INDEX audit_logs_room_idx ON audit_logs (room_id, created_at DESC) WHERE room_id IS NOT NULL;
CREATE INDEX audit_logs_contract_idx ON audit_logs (contract_id, created_at DESC) WHERE contract_id IS NOT NULL;
CREATE INDEX audit_logs_invoice_idx ON audit_logs (invoice_id, created_at DESC) WHERE invoice_id IS NOT NULL;
CREATE INDEX audit_logs_payment_idx ON audit_logs (payment_id, created_at DESC) WHERE payment_id IS NOT NULL;
CREATE INDEX audit_logs_target_user_idx ON audit_logs (target_user_id, created_at DESC) WHERE target_user_id IS NOT NULL;
CREATE INDEX audit_logs_action_idx ON audit_logs (action, created_at DESC);
CREATE INDEX audit_logs_recent_idx ON audit_logs (created_at DESC, id DESC);

CREATE INDEX audit_activity_actor_idx ON audit_activity (actor_id, created_at DESC) WHERE actor_id IS NOT NULL;
CREATE INDEX audit_activity_acting_for_idx ON audit_activity (acting_for_id, created_at DESC) WHERE acting_for_id IS NOT NULL;
CREATE INDEX audit_activity_room_idx ON audit_activity (room_id, created_at DESC) WHERE room_id IS NOT NULL;
CREATE INDEX audit_activity_contract_idx ON audit_activity (contract_id, created_at DESC) WHERE contract_id IS NOT NULL;
CREATE INDEX audit_activity_invoice_idx ON audit_activity (invoice_id, created_at DESC) WHERE invoice_id IS NOT NULL;
CREATE INDEX audit_activity_payment_idx ON audit_activity (payment_id, created_at DESC) WHERE payment_id IS NOT NULL;
CREATE INDEX audit_activity_target_user_idx ON audit_activity (target_user_id, created_at DESC) WHERE target_user_id IS NOT NULL;
CREATE INDEX audit_activity_action_idx ON audit_activity (action, created_at DESC);
CREATE INDEX audit_activity_recent_idx ON audit_activity (created_at DESC, id DESC);

-- The default partition catches whatever falls outside the months made ahead; this month and the next three are made
-- now (the audit-maintain command keeps making them). Months are cut at midnight UTC.
-- +goose StatementBegin
DO $$
DECLARE
    parent text;
    first_day date;
BEGIN
    FOREACH parent IN ARRAY ARRAY['audit_logs', 'audit_activity'] LOOP
        EXECUTE format('CREATE TABLE %I PARTITION OF %I DEFAULT', parent || '_default', parent);
        FOR i IN 0..3 LOOP
            first_day := (date_trunc('month', now() AT TIME ZONE 'UTC') + make_interval(months => i))::date;
            EXECUTE format('CREATE TABLE %I PARTITION OF %I FOR VALUES FROM (%L) TO (%L)',
                parent || '_' || to_char(first_day, 'YYYY_MM'), parent,
                to_char(first_day, 'YYYY-MM-DD') || ' 00:00:00+00',
                to_char(first_day + interval '1 month', 'YYYY-MM-DD') || ' 00:00:00+00');
        END LOOP;
    END LOOP;
END
$$;
-- +goose StatementEnd

-- +goose Down
DROP TABLE audit_activity;
DROP TABLE audit_logs;
