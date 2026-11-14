# Get your API key

kta asks for it the first time you run it. To change it later, run `./kta setup`.

## Linear

1. Open Linear and go to **Settings** (click your workspace name, top left).
2. Under **Account**, open **Security & access**.
3. In **Personal API keys**, click **New API key** and name it `kta`.
4. Leave **Full access** so Claude can also move tickets for you, and create it.
5. Copy the key (it starts with `lin_api_`). Linear shows it only once.

## Jira

Works with Jira Cloud (sites ending in `atlassian.net`).

1. Sign in and open [id.atlassian.com/manage-profile/security/api-tokens](https://id.atlassian.com/manage-profile/security/api-tokens).
2. Click **Create API token**, not the one "with scopes".
3. Name it `kta`, pick when it expires and click **Create**.
4. Copy the token. Atlassian shows it only once.
5. In kta, fill in:
   - **Site**: the address you open Jira at, like `acme.atlassian.net`
   - **Email**: the email you sign in to Atlassian with
   - **API token**: the token you just copied

## Trello

Trello needs two things: an API key and a token.

1. Sign in and open [trello.com/power-ups/admin](https://trello.com/power-ups/admin).
2. Click **New**, name it `kta`, pick your Workspace, fill in the required fields with your own name and email, and click **Create**.
3. Open the **API key** tab, click **Generate a new API key** and copy the **API key**.
4. On that same page, click the **Token** link next to the key, then **Allow**, and copy the token.
   No link? Open `https://trello.com/1/authorize?expiration=never&scope=read,write&response_type=token&key=YOUR_API_KEY`, click **Allow** and copy the token.

## If the board doesn't load

- **"didn't respond" or "can't reach"**: the tracker was slow or your connection dropped. Press `r` on the board to try again.
- **"didn't accept"**: the key or token is wrong or expired. Run `./kta setup` and paste a new one.
