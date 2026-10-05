-- Shared synthetic records for the frozen format fixtures.
INSERT INTO sessions(id,title,workspace,created) VALUES
    ('saved','Saved café','/missing/workspace','2026-10-01T01:00:00Z'),
    ('active','Interrupted work','/missing/other','2026-10-01T02:00:00Z');
INSERT INTO runs VALUES
    ('finished','saved','exited','2026-10-01T01:00:01Z','2026-10-01T01:10:00Z',123,'exit status 7'),
    ('unfinished','active','running','2026-10-01T02:00:01Z','',456,'');
INSERT INTO events(seq,session_id,run_id,kind,data,text,created) VALUES
    (7,'saved','finished','start',CAST('{"command":["/bin/sh","-i"],"workspace":"/missing/workspace","cols":80,"rows":24}' AS BLOB),'','2026-10-01T01:00:01Z'),
    (42,'saved','finished','input',X'68656C6C6F0D0300','','2026-10-01T01:00:02Z'),
    (43,'saved','finished','output',X'1B5B33326D636166C3A91B5B306D0D0A','café','2026-10-01T01:00:03Z'),
    (44,'saved','','attachment',X'0001FF0D0A','{"name":"example.bin","mediaType":"application/octet-stream","size":5}','2026-10-01T01:00:04Z'),
    (45,'saved','','draft',CAST('{"text":"Keep this draft","attachments":[44],"pending":true}' AS BLOB),'','2026-10-01T01:00:05Z'),
    (900,'saved','','notice',X'','deleted high sequence fixture','2026-10-01T01:00:06Z');
DELETE FROM events WHERE seq=900;
