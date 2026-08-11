CREATE OR REPLACE FUNCTION generate_batch_number(p_table_name text, p_service text, p_generated_by text)
RETURNS int
LANGUAGE plpgsql
AS $$
DECLARE
    v_latest_batch_number int;
BEGIN
    SELECT COALESCE(batch_number, 0) + 1
      INTO v_latest_batch_number
      FROM batch_numbers
     WHERE table_name = p_table_name AND service = p_service
     ORDER BY batch_number DESC
     LIMIT 1;

    IF v_latest_batch_number IS NULL OR v_latest_batch_number <= 1 THEN
        RAISE EXCEPTION 'Invalid latest_batch_number --> %', v_latest_batch_number
            USING HINT = 'Investigate why latest batch number did not generate.';
    END IF;

    INSERT INTO batch_numbers(table_name, service, batch_number, generated_by, generated_date)
    VALUES (p_table_name, p_service, v_latest_batch_number, p_generated_by, now());

    RETURN v_latest_batch_number;
END;
$$;
