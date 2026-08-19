import type { Account } from "./types";

export interface AccountGroup {
  key: string;
  email: string | null;
  accounts: Account[];
}

const kindOrder: Record<Account["actualKind"], number> = {
  personal: 0,
  team: 1,
  unknown: 2,
};

export function groupAccounts(accounts: Account[]): AccountGroup[] {
  const groups = new Map<string, AccountGroup>();

  for (const account of accounts) {
    const email = account.email?.trim() || null;
    const key = email
      ? `email:${email.toLowerCase()}`
      : `account:${account.id}`;
    const group = groups.get(key);
    if (group) group.accounts.push(account);
    else groups.set(key, { key, email, accounts: [account] });
  }

  return [...groups.values()]
    .map((group) => ({
      ...group,
      accounts: [...group.accounts].sort(
        (a, b) =>
          kindOrder[a.actualKind] - kindOrder[b.actualKind] || a.id - b.id,
      ),
    }))
    .sort(
      (a, b) =>
        Math.min(...a.accounts.map((account) => account.id)) -
        Math.min(...b.accounts.map((account) => account.id)),
    );
}
