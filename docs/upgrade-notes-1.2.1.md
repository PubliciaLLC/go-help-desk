# Upgrade Notes for v1.2.1

1.2.1 is a small release. Email addresses must now be ASCII.

## Email addresses must be ASCII

New accounts and sign-ins that create or link an account refuse an address containing non-ASCII characters. Signup, administrator create and edit (when the address changes) and guest submission answer `400`; a first-time SAML or OIDC sign-in is refused (`invalid_email` for OIDC, or `domain_not_allowed` when an allowed-domains list is set and the domain is not ASCII). Addresses must be ASCII so that they compare reliably.

- **Existing accounts** keep signing in, by their identity provider subject and by password. An administrator can still rename them or change their role. A sign-in that carries a non-ASCII address keeps the stored one.
- **Allowed domains:** entries must be ASCII. Saving an entry with a non-ASCII character is refused with `400`; use the `xn--` form for an internationalised domain. If you already have such an entry, replace it.
- **Known limitation:** an existing user whose stored address has a non-ASCII domain may be refused at sign-in by an allowed-domains list.

### What to check

```sql
SELECT count(*) FROM users WHERE email ~ '[^\x01-\x7f]';
```

Zero means nothing changes for you.

## Upgrade steps

1. Take a database backup.
2. Run the check above.
3. Restart on 1.2.1. There are no migrations.
