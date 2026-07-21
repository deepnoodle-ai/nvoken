ALTER TABLE tool_calls
    ADD COLUMN result_origin text;

UPDATE tool_calls
SET result_origin = CASE
    WHEN status = 'cancelled' THEN 'system'
    ELSE mode
END
WHERE status IN ('completed', 'failed', 'cancelled');

ALTER TABLE tool_calls
    ADD CONSTRAINT tool_calls_result_origin CHECK (
        result_origin IS NULL OR result_origin IN ('builtin', 'client', 'system')
    ),
    ADD CONSTRAINT tool_calls_result_origin_shape CHECK (
        (status IN ('completed', 'failed', 'cancelled')) = (result_origin IS NOT NULL)
    );

CREATE OR REPLACE FUNCTION nvoken_preserve_tool_call_result_origin()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.result_origin IS NOT NULL
       AND NEW.result_origin IS DISTINCT FROM OLD.result_origin THEN
        RAISE EXCEPTION 'tool call result origin is immutable' USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER tool_calls_preserve_result_origin
    BEFORE UPDATE ON tool_calls
    FOR EACH ROW EXECUTE FUNCTION nvoken_preserve_tool_call_result_origin();
