DROP TRIGGER IF EXISTS hub_licensing_subscriptions_renewal ON hub_licensing_subscriptions;
CREATE TRIGGER hub_licensing_subscriptions_renewal
    AFTER UPDATE OF expires_at ON hub_licensing_subscriptions
    FOR EACH ROW
    WHEN (OLD.tier <> 'free' AND NEW.tier <> 'free' AND NEW.expires_at > OLD.expires_at)
    EXECUTE FUNCTION hub_licensing_record_renewal();

ALTER TABLE hub_licensing_subscriptions
    DROP COLUMN IF EXISTS pre_lifetime_expires_at,
    DROP COLUMN IF EXISTS pre_lifetime_tier,
    DROP COLUMN IF EXISTS lifetime;
