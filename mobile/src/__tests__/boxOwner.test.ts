/* eslint-disable import/first -- the mocks must come before the modules they replace */
// Who may set a new box's first password (#93): the household's owner. A
// phone with no Kergui account (or no household) is the one installing the
// box, so it is the owner. A member gets the owner's number, when the hub
// shows it, to warn them on WhatsApp.
const mockAccount = { account: null as object | null, household: jest.fn() };
jest.mock('../licensing/account', () => ({ useAccount: { getState: () => mockAccount } }));
const mockMembers = jest.fn();
jest.mock('../licensing/household', () => ({ household: { members: () => mockMembers() } }));

import { boxRole } from '../session/boxOwner';

beforeEach(() => {
  mockAccount.account = null;
  mockAccount.household.mockReset();
  mockMembers.mockReset();
});

test('a phone without an account installs its box: it is the owner', async () => {
  await expect(boxRole()).resolves.toEqual({ owner: true });
  expect(mockMembers).not.toHaveBeenCalled();
});

test('an account without a household is the owner too', async () => {
  mockAccount.account = { accountId: 'a' };
  mockAccount.household.mockReturnValue(null);
  await expect(boxRole()).resolves.toEqual({ owner: true });
  expect(mockMembers).not.toHaveBeenCalled();
});

test('the household says who owns it', async () => {
  mockAccount.account = { accountId: 'a' };
  mockAccount.household.mockReturnValue({ tenantId: 'T', userId: 'u' });
  mockMembers.mockResolvedValue({ you: { role: 'owner' }, members: [] });
  await expect(boxRole()).resolves.toEqual({ owner: true });
});

test('a member gets the owner’s number, digits only, when it is not masked', async () => {
  mockAccount.account = { accountId: 'a' };
  mockAccount.household.mockReturnValue({ tenantId: 'T', userId: 'u' });
  mockMembers.mockResolvedValue({
    you: { role: 'admin' },
    members: [{ role: 'owner', phone: '+221 77 000 00 01' }, { role: 'admin', phone: '+221 77 000 00 02' }],
  });
  await expect(boxRole()).resolves.toEqual({ owner: false, ownerPhone: '221770000001' });
  mockMembers.mockResolvedValue({ you: { role: 'viewer' }, members: [{ role: 'owner', phone: '+221 77 *** ** 01' }] });
  await expect(boxRole()).resolves.toEqual({ owner: false, ownerPhone: null });
});

test('a hub that cannot be reached does not stop the one installing the box', async () => {
  // A new box often has no Internet yet: the label's holder goes on.
  mockAccount.account = { accountId: 'a' };
  mockAccount.household.mockReturnValue({ tenantId: 'T', userId: 'u' });
  mockMembers.mockRejectedValue(new Error('offline'));
  await expect(boxRole()).resolves.toEqual({ owner: true });
});
