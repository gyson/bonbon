CREATE INDEX events_run ON events(run_id,seq);
CREATE INDEX events_output_run ON events(run_id,seq) WHERE kind='output' AND text<>'';
CREATE INDEX events_start_run ON events(run_id,seq) WHERE kind='start';
