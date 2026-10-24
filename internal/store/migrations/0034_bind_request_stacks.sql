ALTER TABLE bind_requests ADD COLUMN stack_id INTEGER REFERENCES stacks(id) ON DELETE SET NULL;
CREATE UNIQUE INDEX bind_request_stack ON bind_requests(stack_id) WHERE stack_id IS NOT NULL;
