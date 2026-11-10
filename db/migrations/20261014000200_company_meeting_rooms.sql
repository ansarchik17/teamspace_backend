-- +goose Up
CREATE TABLE meeting_sites (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
 name varchar(255) NOT NULL, address text NOT NULL DEFAULT '', city varchar(255) NOT NULL DEFAULT '',
 timezone varchar(100) NOT NULL DEFAULT 'Asia/Almaty', working_hours varchar(100) NOT NULL DEFAULT '',
 is_active boolean NOT NULL DEFAULT true, created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now(), deleted_at timestamptz,
 UNIQUE(organization_id,id)
);
ALTER TABLE meeting_rooms ADD COLUMN organization_id uuid REFERENCES organizations(id),
 ADD COLUMN site_id uuid, ADD COLUMN photo_url text,
 ADD CONSTRAINT meeting_room_site_tenant FOREIGN KEY(organization_id,site_id) REFERENCES meeting_sites(organization_id,id);
CREATE INDEX meeting_rooms_organization ON meeting_rooms(organization_id) WHERE deleted_at IS NULL;
INSERT INTO meeting_sites(organization_id,name) SELECT id,'Основной офис' FROM organizations;

-- Assign only rooms whose historical organizers all belong to one known company.
-- Unbooked or ambiguous rooms remain unassigned and are disabled, never guessed.
WITH owners AS (
 SELECT rb.room_id,u.organization_id FROM room_bookings rb JOIN users u ON u.id=rb.user_id
 UNION SELECT e.room_id,u.organization_id FROM events e JOIN users u ON u.id=e.user_id WHERE e.room_id IS NOT NULL
), resolved AS (
 SELECT room_id,min(organization_id::text)::uuid AS organization_id FROM owners
 GROUP BY room_id HAVING count(DISTINCT organization_id)=1 AND count(*)=count(organization_id)
)
UPDATE meeting_rooms r SET organization_id=x.organization_id,site_id=s.id
 FROM resolved x JOIN meeting_sites s ON s.organization_id=x.organization_id
 WHERE r.id=x.room_id;
UPDATE meeting_rooms SET is_active=false WHERE organization_id IS NULL;
CREATE VIEW calendar_unassigned_rooms AS SELECT id,name,location,capacity FROM meeting_rooms WHERE organization_id IS NULL AND deleted_at IS NULL;

ALTER TABLE events ADD COLUMN room_snapshot jsonb;
UPDATE events e SET room_snapshot=to_jsonb(r)||jsonb_build_object('site_name',s.name,'address',s.address)
 FROM meeting_rooms r LEFT JOIN meeting_sites s ON s.id=r.site_id WHERE e.room_id=r.id;
UPDATE event_exceptions x SET event_data=x.event_data||jsonb_build_object('room',
 to_jsonb(r)||jsonb_build_object('site_name',s.name,'address',s.address))
 FROM meeting_rooms r LEFT JOIN meeting_sites s ON s.id=r.site_id
 WHERE x.exception_type='modified' AND x.event_data->>'room_id'=r.id::text;
-- Disambiguate legacy duplicate names without changing the booking snapshot above.
WITH duplicates AS (SELECT id,row_number() OVER(PARTITION BY site_id,lower(btrim(name)) ORDER BY created_at,id) AS n FROM meeting_rooms WHERE deleted_at IS NULL AND site_id IS NOT NULL)
UPDATE meeting_rooms r SET name=left(r.name,215)||' ('||r.id::text||')' FROM duplicates d WHERE r.id=d.id AND d.n>1;
CREATE UNIQUE INDEX meeting_room_site_name ON meeting_rooms(site_id,lower(btrim(name))) WHERE deleted_at IS NULL AND site_id IS NOT NULL;
ALTER TABLE event_attendees ADD COLUMN attendance text CHECK(attendance IN ('offline','online'));
UPDATE event_attendees a SET attendance=CASE
 WHEN (e.room_id IS NOT NULL OR NULLIF(btrim(e.location),'') IS NOT NULL) AND NOT (e.is_online AND NULLIF(btrim(e.meeting_url),'') IS NOT NULL) THEN 'offline'
 WHEN e.room_id IS NULL AND NULLIF(btrim(e.location),'') IS NULL AND e.is_online AND NULLIF(btrim(e.meeting_url),'') IS NOT NULL THEN 'online'
 ELSE NULL END FROM events e WHERE e.id=a.event_id;

CREATE TABLE calendar_meeting_changes (
 id bigserial PRIMARY KEY, event_id uuid REFERENCES events(id) ON DELETE CASCADE,
 user_id bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE, kind text NOT NULL,
 metadata jsonb NOT NULL, created_at timestamptz NOT NULL DEFAULT now(), delivered_at timestamptz, locked_until timestamptz
);
CREATE INDEX calendar_meeting_changes_pending ON calendar_meeting_changes(id) WHERE delivered_at IS NULL;

-- +goose StatementBegin
CREATE FUNCTION calendar_capture_room() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='UPDATE' AND NEW.room_id IS NOT DISTINCT FROM OLD.room_id THEN RETURN NEW; END IF;
 IF NEW.room_id IS NULL THEN NEW.room_snapshot:=NULL; RETURN NEW; END IF;
 SELECT to_jsonb(r)||jsonb_build_object('site_name',s.name,'address',s.address) INTO NEW.room_snapshot
 FROM meeting_rooms r LEFT JOIN meeting_sites s ON s.id=r.site_id WHERE r.id=NEW.room_id;
 RETURN NEW;
END $$;
CREATE TRIGGER calendar_capture_room BEFORE INSERT OR UPDATE OF room_id ON events FOR EACH ROW EXECUTE FUNCTION calendar_capture_room();

CREATE FUNCTION calendar_attendance_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE place boolean; link boolean;
BEGIN
 SELECT (room_id IS NOT NULL OR NULLIF(btrim(location),'') IS NOT NULL),
 (is_online AND NULLIF(btrim(meeting_url),'') IS NOT NULL) INTO place,link FROM events WHERE id=NEW.event_id FOR SHARE;
 IF NEW.attendance='offline' AND NOT place OR NEW.attendance='online' AND NOT link THEN
  RAISE EXCEPTION 'Attendance unavailable for event' USING ERRCODE='P7601';
 END IF;
 IF TG_OP='INSERT' AND NEW.attendance IS NULL THEN
  IF place AND NOT link THEN NEW.attendance:='offline'; ELSIF link AND NOT place THEN NEW.attendance:='online'; END IF;
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER calendar_attendance_guard BEFORE INSERT OR UPDATE OF attendance ON event_attendees FOR EACH ROW EXECUTE FUNCTION calendar_attendance_guard();

CREATE FUNCTION calendar_place_changed() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF ROW(NEW.room_id,NEW.location,NEW.is_online,NEW.meeting_url) IS NOT DISTINCT FROM ROW(OLD.room_id,OLD.location,OLD.is_online,OLD.meeting_url) THEN RETURN NEW; END IF;
 UPDATE event_attendees SET attendance=NULL,updated_at=now() WHERE event_id=NEW.id AND (
 (attendance='online' AND NOT (NEW.is_online AND NULLIF(btrim(NEW.meeting_url),'') IS NOT NULL)) OR
 (attendance='offline' AND NEW.room_id IS NULL AND NULLIF(btrim(NEW.location),'') IS NULL));
 INSERT INTO calendar_meeting_changes(event_id,user_id,kind,metadata)
 SELECT NEW.id,user_id,'event.location_changed',jsonb_build_object('event_id',NEW.id::text,'event_title',NEW.title,
 'location',CASE WHEN NEW.room_snapshot IS NOT NULL THEN concat_ws(', ',NEW.room_snapshot->>'name',
 NULLIF(NEW.room_snapshot->>'site_name',''),NULLIF(NEW.room_snapshot->>'address',''),
 CASE WHEN NEW.room_snapshot->>'floor' IS NOT NULL THEN (NEW.room_snapshot->>'floor')||' этаж' END,
 NULLIF(NEW.room_snapshot->>'location','')) ELSE COALESCE(NEW.location,'') END,
 'meeting_url',CASE WHEN NEW.is_online THEN COALESCE(NEW.meeting_url,'') ELSE '' END)
 FROM event_attendees WHERE event_id=NEW.id;
 RETURN NEW;
END $$;
CREATE TRIGGER calendar_place_changed AFTER UPDATE OF room_id,location,is_online,meeting_url ON events FOR EACH ROW EXECUTE FUNCTION calendar_place_changed();

CREATE FUNCTION calendar_room_booking_tenant_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE actor_org uuid; room_org uuid; active boolean;
BEGIN
 SELECT organization_id INTO actor_org FROM users WHERE id=NEW.user_id AND is_active AND deleted_at IS NULL FOR SHARE;
 SELECT r.organization_id,r.is_active AND r.deleted_at IS NULL AND s.is_active AND s.deleted_at IS NULL INTO room_org,active
 FROM meeting_rooms r JOIN meeting_sites s ON s.id=r.site_id WHERE r.id=NEW.room_id FOR SHARE OF r,s;
 IF actor_org IS NULL OR room_org IS DISTINCT FROM actor_org THEN RAISE EXCEPTION 'Room not found' USING ERRCODE='P7602'; END IF;
 IF active IS DISTINCT FROM true THEN RAISE EXCEPTION 'Room inactive' USING ERRCODE='P7603'; END IF;
 IF NEW.event_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM events WHERE id=NEW.event_id AND user_id=NEW.user_id AND deleted_at IS NULL) THEN
  RAISE EXCEPTION 'Event not found' USING ERRCODE='P7602';
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER calendar_room_booking_tenant_guard BEFORE INSERT OR UPDATE OF room_id,user_id,start_time,end_time ON room_bookings
 FOR EACH ROW WHEN (NEW.status='confirmed' AND NEW.deleted_at IS NULL) EXECUTE FUNCTION calendar_room_booking_tenant_guard();

CREATE FUNCTION calendar_disabled_room_notice() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF OLD.is_active AND NOT NEW.is_active THEN
 INSERT INTO calendar_meeting_changes(event_id,user_id,kind,metadata)
 SELECT DISTINCT b.event_id,b.user_id,'room.disabled',jsonb_build_object('room_id',r.id::text,'room_name',r.name,'event_id',COALESCE(b.event_id::text,''))
 FROM room_bookings b JOIN meeting_rooms r ON r.id=b.room_id
 WHERE b.status='confirmed' AND b.deleted_at IS NULL AND b.end_time>now() AND
 ((TG_TABLE_NAME='meeting_rooms' AND r.id=NEW.id) OR (TG_TABLE_NAME='meeting_sites' AND r.site_id=NEW.id));
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER calendar_disabled_room_notice AFTER UPDATE OF is_active ON meeting_rooms FOR EACH ROW EXECUTE FUNCTION calendar_disabled_room_notice();
CREATE TRIGGER calendar_disabled_site_notice AFTER UPDATE OF is_active ON meeting_sites FOR EACH ROW EXECUTE FUNCTION calendar_disabled_room_notice();
-- +goose StatementEnd

ALTER TABLE organization_position_permissions DROP CONSTRAINT organization_position_permissions_application_check;
ALTER TABLE organization_position_permissions ADD CONSTRAINT organization_position_permissions_application_check CHECK(application IN ('odo','sd','iform','calendar'));
ALTER TABLE organization_access_apps DROP CONSTRAINT organization_access_apps_application_check;
ALTER TABLE organization_access_apps ADD CONSTRAINT organization_access_apps_application_check CHECK(application IN ('odo','sd','iform','calendar'));

-- +goose Down
DROP TRIGGER calendar_disabled_site_notice ON meeting_sites;
DROP TRIGGER calendar_disabled_room_notice ON meeting_rooms;
DROP TRIGGER calendar_room_booking_tenant_guard ON room_bookings;
DROP TRIGGER calendar_place_changed ON events;
DROP TRIGGER calendar_attendance_guard ON event_attendees;
DROP TRIGGER calendar_capture_room ON events;
DROP FUNCTION calendar_disabled_room_notice(),calendar_room_booking_tenant_guard(),calendar_place_changed(),calendar_attendance_guard(),calendar_capture_room();
DROP TABLE calendar_meeting_changes;
ALTER TABLE event_attendees DROP COLUMN attendance;
ALTER TABLE events DROP COLUMN room_snapshot;
DROP VIEW calendar_unassigned_rooms;
DROP INDEX meeting_room_site_name;
ALTER TABLE meeting_rooms DROP CONSTRAINT meeting_room_site_tenant,DROP COLUMN organization_id,DROP COLUMN site_id,DROP COLUMN photo_url;
DROP TABLE meeting_sites;
DELETE FROM organization_position_permissions WHERE application='calendar';
DELETE FROM organization_access_apps WHERE application='calendar';
ALTER TABLE organization_position_permissions DROP CONSTRAINT organization_position_permissions_application_check;
ALTER TABLE organization_position_permissions ADD CONSTRAINT organization_position_permissions_application_check CHECK(application IN ('odo','sd','iform'));
ALTER TABLE organization_access_apps DROP CONSTRAINT organization_access_apps_application_check;
ALTER TABLE organization_access_apps ADD CONSTRAINT organization_access_apps_application_check CHECK(application IN ('odo','sd','iform'));
