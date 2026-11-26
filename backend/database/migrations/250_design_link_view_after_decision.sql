-- Opening a design approval link updates first_opened_at. That update must not
-- be treated as a second decision once the client has already chosen.
CREATE OR REPLACE FUNCTION app.tg_design_link_decide() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
BEGIN
  IF OLD.decision IS NOT NULL AND NEW.decision IS DISTINCT FROM OLD.decision THEN
    RAISE EXCEPTION 'this approval link has already been used' USING ERRCODE = 'BM004';
  END IF;
  IF NEW.decision IS NOT NULL AND OLD.decision IS NULL THEN
    IF OLD.revoked_at IS NOT NULL OR OLD.expires_at <= now() THEN
      RAISE EXCEPTION 'approval link expired or revoked' USING ERRCODE = 'BM001';
    END IF;
    NEW.decided_at := now();
    UPDATE app.design_versions SET status = NEW.decision::text::app.design_version_status
     WHERE tenant_id = NEW.tenant_id AND id = NEW.design_version_id AND status = 'sent_to_client';
    IF NOT FOUND THEN
      RAISE EXCEPTION 'version is no longer awaiting the client' USING ERRCODE = 'BM001';
    END IF;
  END IF;
  RETURN NEW;
END $$;
