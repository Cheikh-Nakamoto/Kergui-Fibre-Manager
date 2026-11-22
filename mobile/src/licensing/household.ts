import { useAccount } from './account';
import { HubError, type HouseholdMembers, type MemberRole } from './api';
import { hub } from './hub';

// The household's members (issue #42), on the hub's
// /api/v1/tenants/{tenantId}/members with the kernel access token. The
// household is the one the sign-in landed in (hubTokens.ts); a member who
// was removed from it gets a 403, and is signed in again to the household
// that is theirs now.

async function withHousehold<T>(run: (token: string, tenantId: string) => Promise<T>): Promise<T> {
  const account = useAccount.getState();
  const token = await account.hubAccessToken();
  const held = account.household() ?? (await account.rejoinHousehold());
  if (!held) throw new HubError(0, 'NOT_CONFIGURED', 'no household on this hub');
  try {
    return await run(token, held.tenantId);
  } catch (e) {
    if (!(e instanceof HubError) || e.status !== 403) throw e;
    // Out of that household (removed), or into another (invited): the
    // licensing session says which one is ours now, once.
    const now = await account.rejoinHousehold();
    if (!now || now.tenantId === held.tenantId) throw e;
    return run(await account.hubAccessToken(), now.tenantId);
  }
}

export const householdKey = ['hub', 'members'] as const;

export const household = {
  members: (): Promise<HouseholdMembers> => withHousehold((tok, id) => hub.members(tok, id)),
  invite: (phone: string, role: MemberRole) => withHousehold((tok, id) => hub.inviteMember(tok, id, phone, role)),
  cancelInvite: (inviteId: string) => withHousehold((tok, id) => hub.cancelInvite(tok, id, inviteId)),
  changeRole: (membershipId: string, role: MemberRole) => withHousehold((tok, id) => hub.changeRole(tok, id, membershipId, role)),
  remove: (membershipId: string) => withHousehold((tok, id) => hub.removeMember(tok, id, membershipId)),
};

/**
 * What the demo shows: a made-up household, marked as such, so the store
 * reviewer and the Maestro flow see the screen without a hub account.
 */
export const DEMO_HOUSEHOLD: HouseholdMembers = {
  household: { id: 'demo', name: 'Foyer de démonstration' },
  you: { membershipId: 'demo-1', userId: 'demo-you', role: 'owner', phone: '+221 77 000 00 01' },
  members: [
    { membershipId: 'demo-1', userId: 'demo-you', role: 'owner', phone: '+221 77 000 00 01' },
    { membershipId: 'demo-2', userId: 'demo-awa', role: 'admin', phone: '+221 77 000 00 02' },
    { membershipId: 'demo-3', userId: 'demo-moussa', role: 'viewer', phone: '+221 77 000 00 03' },
  ],
  invites: [],
};
