# Setup

This document describes how to run Robin: the Slack app, Google Cloud (Cloud KMS
and Firestore), the optional Google Workspace, Notion, and GitHub
integrations, Claude, the server configuration, and local development.

## How Robin answers mentions

When someone mentions Robin, Robin answers in the thread of the mention with
Claude. Claude reads the services the person has connected (Slack search,
Notion, Google Workspace, GitHub) with read-only tools that use that person's
own tokens.

- **One conversation per thread.** The first person who mentions Robin in a
  thread starts the conversation and owns it. Their later mentions in the same
  thread continue it: Claude sees the earlier requests, the tool results and
  the answers. Robin answers nobody else in that thread; they get a message
  only they can see that asks them to start their own thread. A conversation
  ends 30 days after it started (`[agent] session_ttl`); the next person who
  mentions Robin in that thread then starts a new one.
- **Progress message.** Right after a mention, Robin posts one small message
  in the thread and keeps replacing its text with what it is doing (for
  example "Searching Notion for …"). When the run ends, it shows the result,
  the number of model and tool calls, and the cost.
- **Cost limit.** Each mention has a budget, `[agent] budget_usd` (default
  $2.00). Claude sees the spending so far with every call. When the spending
  reaches 90% of the budget, Robin asks Claude to answer with what it has,
  without more tools. Robin does not start a model call once the spending has
  reached the budget, but it cannot stop a call that is already running, so
  that call can take the total above the budget. Robin also asks Claude to
  answer at the 20th model call and when the material gathered for one answer
  reaches 8 MiB, and gives up after 10 minutes.
- **Private Slack content.** The Slack search finds everything the person can
  see, including private channels, DMs and group DMs. The answer is posted in
  the thread, where everyone in the channel can read it, so Claude is told not
  to quote or copy messages from private channels, DMs or group DMs and to
  summarize and link to them instead. This is an instruction to the model,
  not a filter on the answer.
- **Deleting Robin's messages.** Robin's messages can be deleted with the
  message shortcut **Delete Robin message** in the **More actions** menu of a
  message. Only the person whose mention made Robin post the message can
  delete it. Slack shows the shortcut on every message; on a message Robin did
  not post, Robin only explains that it cannot delete it. Deleting a message
  does not change the stored conversation.

## How Robin posts scheduled messages

On the settings page, under **Scheduled messages**, each user can ask Robin to
post a short greeting written by Claude to a Slack channel every day at a time
and in a time zone they choose. Each of these is a *job*.

- **Adding a job.** The user enters the channel ID (shown at the bottom of the
  channel details in Slack), the time and the time zone. Robin adds the job
  only when the channel exists, is not archived, and has both Robin and the
  user as members. A user can have up to 10 jobs and sees and deletes only
  their own.
- **Running jobs.** `robin schedule` ([section 9](#9-run-the-scheduler)) runs
  every job whose time has come, once, and exits. Run it every few minutes,
  for example every 5 minutes; a greeting is posted at the first run of the
  command after its time. When the command does not run within one hour of a
  job's time (`--max-delay`), that day's greeting is skipped, and days the
  command did not run at all are not made up.
- **Once per day at most.** Several `robin schedule` processes may run at the
  same time: each day's greeting of a job is started by one of them only,
  decided in a Firestore transaction. A greeting whose process stops while it
  is being written is not retried, so it is posted at most once.
- **The greeting.** Claude writes one or two sentences in English with the
  model and prices of `[llm]` ([section 7](#7-set-up-claude)), in one call with
  a limit of 2,048 output tokens, and Robin posts it to the channel. The user
  who added the job can delete the message with **Delete Robin message**.
- **Results.** The settings page shows each job's next post and how the last
  one ended: posted, failed, or skipped. When the result of a post was not
  recorded (the process stopped, or the result could not be saved), the page
  says so and asks the user to check the channel. Robin keeps the record
  of each run, including its cost, for 30 days.

## How Robin uses Slack

- **Bot token** (`xoxb-`): installed once by a workspace administrator. Robin
  uses it to receive mentions, read the thread of a mention, post, update and
  delete its own messages in threads, send login prompts, read the display
  name of a user who signs in, and, when a user adds a scheduled message,
  check the channel and its members and later post to it.
- **User tokens** (`xoxp-`): obtained from each user when they sign in on the
  web page. The sign-in is a Slack OAuth v2 authorization that requests the user
  scope `search:read`, so one authorization both identifies the user and
  returns the user's token. Robin encrypts the token with Cloud KMS and stores
  only the ciphertext in Firestore. A token is used only for requests made by
  the user who owns it: Claude searches Slack with the token of the person who
  mentioned Robin.
- A user who mentions the bot without having signed in receives a message that
  only they can see, with a link to the sign-in page. Signing out on the web
  ends only that browser's session; the stored user token stays, so the bot
  keeps answering that user's mentions.

## How Robin uses Google Workspace

The Google Workspace integration is optional and is enabled only when the
server has a Google OAuth client (step 4). It is meant for one Google Workspace
organization: the OAuth client belongs to that organization and admits only
its accounts.

- Each user connects their own Google account on the settings page. Robin asks
  Google for these scopes:

  | Scope | Used for |
  | --- | --- |
  | `openid`, `email` | Identify the connected Google account and show its address on the settings page |
  | `https://www.googleapis.com/auth/calendar.readonly` | Read the user's calendars and events |
  | `https://www.googleapis.com/auth/drive.readonly` | Search Drive and read files, including Google Docs, Sheets, and Slides |
  | `https://www.googleapis.com/auth/gmail.readonly` | Search and read the user's mail |

  No scope allows creating, changing, deleting, or sending anything. Claude
  uses this access to answer mentions: it searches and reads mail, searches
  Drive and reads Google Docs, Sheets (as CSV), Slides and text files, and
  lists the events of the primary calendar. Other file types, such as PDF,
  are not read.
- Google lets a user uncheck individual permissions on the consent screen. If
  the Calendar, Drive, or Gmail permission is not granted, Robin revokes the
  grant at Google, stores nothing, and tells the user to connect again.
- Robin connects the Google account that the user authorizes on Google; it
  does not compare that account with the user's Slack account.
- One Google account can be connected to only one Robin user. Google revokes a
  grant per Google account and Cloud project, not per token, so if two users
  shared one Google account, one user's disconnection would end the other's
  access. A user who authorizes an account that is already connected to someone
  else is told so; Robin stores nothing and leaves that account's grant as it
  is.
- A user who is already connected cannot connect a second account: the
  connection is ignored and the settings page stays as it is. Disconnect first
  to switch accounts.
- Robin stores only the refresh token, encrypted with Cloud KMS, together with
  the granted scopes and the account's address. Access tokens are not stored.
- **Disconnect Google Workspace** on the settings page revokes the grant at
  Google and deletes the stored token. Signing out of Robin does not disconnect
  Google Workspace. A user can also remove Robin from the third-party apps
  list of their Google account.
- Google stops accepting a refresh token when the user revokes access, when it
  has not been used for six months, when the user changes their password (the
  Gmail scope is included), or when an administrator restricts one of the
  services. The settings page does not ask Google whether the stored token is
  still accepted, so it keeps showing **Connected** in these cases; disconnect
  and connect again to obtain a new token. When Google rejects the token while
  Claude reads, Claude tells the person to do that.

## How Robin uses Notion

The Notion integration is optional and is enabled only when the server has a
Notion public integration (step 5). It is meant for one Notion workspace: the
integration can be installed only in that workspace, and Robin also rejects a
connection to any other workspace.

- Each user connects Notion on the settings page. On Notion's authorization
  page, the user picks the pages and databases to share with Robin. Robin can
  read only those pages and databases and the pages under them, not the whole
  workspace. The user can share more pages, or stop sharing them, later from
  the **Connections** menu of a Notion page.
- The integration has the **Read content** capability only. Robin can search
  the shared pages and databases, read pages and their content, and read and
  query databases. It cannot create, change, or comment on anything. Claude
  uses this access to answer mentions.
- Robin stores the Notion user who authorized, as returned by Notion; it does
  not compare it with the Slack account.
- One Notion user can be connected to only one Robin user. Whether Notion
  treats two authorizations by the same Notion user as one connection is not
  documented; if it does, one Robin user's disconnection would end the other's
  access. A user who authorizes a Notion user that is already connected to
  someone else is told so, and Robin stores nothing.
- A user who is already connected cannot connect again: the connection is
  ignored and the settings page stays as it is. Disconnect first to switch
  accounts.
- Notion issues an access token and a refresh token. Robin stores both,
  encrypted with Cloud KMS as one ciphertext, together with the workspace and
  the Notion user's name. When Notion rejects the access token, Robin obtains a
  new pair with the refresh token and stores it. When Notion rejects the
  refresh token too, for example because the user removed Robin from their
  Notion connections, the settings page shows **Reconnect required**;
  **Reconnect Notion** authorizes again and replaces the stored tokens.
- **Disconnect Notion** on the settings page revokes the access token at Notion
  and deletes the stored tokens. Signing out of Robin does not disconnect
  Notion.

## How Robin uses GitHub

The GitHub integration is optional and is enabled only when the server has a
GitHub App (step 6). Each deployment creates its own GitHub App in the
organization that uses Robin.

- Each user connects their own GitHub account on the settings page. Robin
  obtains a user access token of the GitHub App, so it acts with that user's
  own permissions. The token reaches only resources that satisfy all three
  conditions: the user can access them, the app has the permission for them
  (the permission table in step 6), and the app is installed on the account
  that owns them. A repository of an organization where the app is not
  installed stays unreadable even after the user connects.
- The app gets read permissions only. Claude uses this access to answer
  mentions: it searches issues, pull requests and code, and reads issues with
  their comments and repository files.
- Robin does not use the app's private key or installation tokens, so it
  never reads GitHub on behalf of the app itself.
- Robin connects the GitHub account that the user authorizes on GitHub; it
  does not compare that account with the user's Slack account.
- One GitHub account can be connected to only one Robin user. GitHub deletes an
  app authorization per GitHub account, not per token, so if two users shared
  one GitHub account, one user's disconnection would end the other's access. A
  user who authorizes an account that is already connected to someone else is
  told so, and Robin stores nothing.
- A user who is already connected cannot connect a second account: the
  connection is ignored and the settings page stays as it is. Disconnect first
  to switch accounts.
- The access token expires after eight hours and the refresh token after six
  months (GitHub's default for GitHub Apps). Robin stores both, encrypted with
  Cloud KMS, and refreshes the access token when it is used within five
  minutes of expiring. A refresh token can be used only once, so when several
  instances run, only one of them refreshes at a time. A connection that has
  not been used for six months cannot be refreshed any more; the settings page
  shows it as **Not connected**, and the user connects again.
- **Disconnect GitHub** on the settings page deletes the user's authorization of
  the app at GitHub, which ends every token of it, and then deletes the stored
  tokens. When GitHub cannot be reached, nothing is deleted and the page asks
  the user to try again. Signing out of Robin does not disconnect GitHub.
- A user can also revoke the app under **Settings** → **Applications** →
  **Authorized GitHub Apps** on GitHub. Robin does not receive a notification
  of that, so the settings page keeps showing **Connected** until the tokens
  are next refreshed or the user disconnects.

## 1. Create the Slack app

1. Copy `docs/slack-app-manifest.yaml` and replace `robin.example.com` with the
   public URL of your server (the value of `ROBIN_BASE_URL`). The redirect URL
   (`/api/v1/auth/callback`), the event request URL (`/hooks/slack/event`) and
   the interactivity request URL (`/hooks/slack/interaction`, used by the
   **Delete Robin message** shortcut) must use that host. A Slack app created before the API moved under
   `/api/v1` has `/api/auth/callback` as its redirect URL; change it on **OAuth &
   Permissions** → **Redirect URLs**, or sign-in fails.
2. Open https://api.slack.com/apps, choose **Create New App** → **From an app
   manifest**, select your workspace, and paste the manifest.
3. On **Basic Information** → **Display Information**, upload
   `docs/images/robin.png` (1110 × 1110 PNG) as the **App icon**. The manifest
   cannot set the icon.
4. On **Install App**, install the app to the workspace. Copy the **Bot User
   OAuth Token** (`xoxb-...`) → `ROBIN_SLACK_BOT_TOKEN`.
5. On **Basic Information** → **App Credentials**, copy:
   - **Client ID** → `ROBIN_SLACK_CLIENT_ID`
   - **Client Secret** → `ROBIN_SLACK_CLIENT_SECRET`
   - **Signing Secret** → `ROBIN_SLACK_SIGNING_SECRET`
6. Find the workspace ID (starts with `T`) → `ROBIN_SLACK_TEAM_ID`. It is shown
   in the workspace URL of the Slack web client (`https://app.slack.com/client/T.../...`).
7. The event request URL is verified by Slack only when the server is running.
   Start the server ([section 8](#8-run-the-server)) and re-verify the URL on **Event Subscriptions** if
   Slack reported it as unverified.
8. Invite the bot to the channels where it should answer or post scheduled
   messages (`/invite @robin`).

An app created from an older manifest lacks the bot scopes `channels:history`,
`groups:history` and `mpim:history` (Robin reads the thread of a mention with
them), `channels:read` and `groups:read` (Robin checks the channel of a
scheduled message with them), the **Delete Robin message** shortcut and the
interactivity request URL. Update the app's manifest on **App Manifest** with
the current file, then reinstall the app on **Install App** so the new scopes
take effect. Without the history scopes, every answer fails when Robin reads
the thread; without `channels:read` and `groups:read`, adding a scheduled
message fails.

Keep **Token Rotation** disabled (the manifest sets
`token_rotation_enabled: false`). Slack does not allow turning it off once it is
enabled, and Robin stores long-lived user tokens.

## 2. Create the Cloud KMS key

Robin encrypts user tokens with a symmetric Cloud KMS key. The key name passed to
Robin is the crypto key, not a key version.

```sh
gcloud kms keyrings create robin --location=global --project=$PROJECT_ID
gcloud kms keys create slack-user-token \
  --keyring=robin --location=global --purpose=encryption --project=$PROJECT_ID

# Allow the service account that runs Robin to encrypt and decrypt with this key.
gcloud kms keys add-iam-policy-binding slack-user-token \
  --keyring=robin --location=global --project=$PROJECT_ID \
  --member=serviceAccount:$SERVICE_ACCOUNT \
  --role=roles/cloudkms.cryptoKeyEncrypterDecrypter
```

`ROBIN_KMS_KEY_NAME` is then
`projects/$PROJECT_ID/locations/global/keyRings/robin/cryptoKeys/slack-user-token`.

The same key encrypts the Google refresh tokens of the Google Workspace
integration, the Notion tokens of the Notion integration, and the user tokens
of the GitHub integration.

Each ciphertext is bound to its owner through additional authenticated data
(`robin:slack-user-token:v1:{TeamID}:{UserID}` for Slack,
`robin:google-refresh-token:v1:{TeamID}:{UserID}` for Google,
`robin:notion-token:v1:{TeamID}:{UserID}` for Notion,
`robin:github-access-token:v1:{TeamID}:{UserID}` and
`robin:github-refresh-token:v1:{TeamID}:{UserID}` for GitHub), so a ciphertext
copied into another user's document cannot be decrypted. Do not disable or
destroy the key versions that encrypted stored tokens: those tokens become
unreadable, the affected users have to sign in again, and they cannot
disconnect Google Workspace, Notion, or GitHub until the key version is
restored.

## 3. Prepare Firestore

1. Create a Firestore database in Native mode. Robin uses the `(default)`
   database unless `ROBIN_FIRESTORE_DATABASE_ID` is set.
2. Grant the service account `roles/datastore.user`.
3. Enable TTL policies so expired documents are deleted:

   ```sh
   gcloud firestore fields ttls update ExpiresAt --collection-group=sessions --enable-ttl --project=$PROJECT_ID
   gcloud firestore fields ttls update ExpiresAt --collection-group=slackEvents --enable-ttl --project=$PROJECT_ID
   gcloud firestore fields ttls update ExpiresAt --collection-group=agentSessions --enable-ttl --project=$PROJECT_ID
   gcloud firestore fields ttls update ExpiresAt --collection-group=agentSessionMessages --enable-ttl --project=$PROJECT_ID
   gcloud firestore fields ttls update ExpiresAt --collection-group=agentThreads --enable-ttl --project=$PROJECT_ID
   gcloud firestore fields ttls update ExpiresAt --collection-group=jobRuns --enable-ttl --project=$PROJECT_ID
   ```

   TTL deletion runs some time after the expiry; Robin also checks the expiry
   itself, so an expired session is rejected even before it is deleted.

No composite index is needed. Documents are laid out as follows:

| Path | Content |
| --- | --- |
| `teams/{TeamID}/users/{UserID}` | User (display name, timestamps) |
| `teams/{TeamID}/users/{UserID}/credentials/slack` | Encrypted Slack user token and granted scopes |
| `teams/{TeamID}/users/{UserID}/credentials/google_workspace` | Encrypted Google refresh token, granted scopes, and the connected Google account (ID and address) |
| `googleWorkspaceAccounts/{GoogleAccountID}` | The only Robin user a Google account is connected to. Created and deleted together with the credential above |
| `teams/{TeamID}/users/{UserID}/credentials/notion` | Encrypted Notion access and refresh tokens, the workspace (ID and name), the Notion user (ID and name), and whether the user has to reconnect |
| `notionAccounts/{NotionUserID}` | The only Robin user a Notion user is connected to. Written and deleted together with the credential above |
| `teams/{TeamID}/users/{UserID}/credentials/github` | Encrypted GitHub access and refresh tokens, their expiry, the connected GitHub account (ID and login), and the lease that lets one instance refresh at a time |
| `githubAccounts/{GitHubUserID}` | The only Robin user a GitHub account is connected to. Created and deleted together with the credential above |
| `teams/{TeamID}/users/{UserID}/agentSessions/{AgentSessionID}` | Conversation of one Slack thread with the user who started it: the channel and thread, the number of stored messages, the last answered mention, the lease of a running answer, and the expiry. `AgentSessionID` is `{TeamID}-{ChannelID}-{thread ts without the dot}` |
| `teams/{TeamID}/users/{UserID}/agentSessions/{AgentSessionID}/agentSessionMessages/{Generation}-{Seq}` | One message of the conversation (the request, Robin's notes to the model, the model's response and tool results) in the format of the model's API |
| `agentThreads/{AgentSessionID}` | The only Robin user who owns the conversation of a thread. Written together with the conversation above |
| `teams/{TeamID}/users/{UserID}/jobs/{JobID}` | One scheduled message of the user: its kind, the channel (ID and name at the time it was added), the time and time zone, the next run, and how the last run ended |
| `teams/{TeamID}/users/{UserID}/jobs/{JobID}/jobRuns/{RunID}` | One run of the job (`RunID` is its scheduled time in UTC, such as `20261005T000000Z`): when it started and ended, its result, the posted message, and its cost (kept 30 days) |
| `schedules/{JobID}` | The owner and the next run time of a job, read by `robin schedule` to find jobs whose time has come. Written and deleted together with the job above |
| `sessions/{SessionID}` | Web session (hash of the session secret, owner, expiry) |
| `slackEvents/{EventID}` | Record of a processed Slack event, used to drop redelivered events (kept 24 hours) |

Everything that belongs to a user is stored under that user's document path.
`googleWorkspaceAccounts`, `notionAccounts`, `githubAccounts`, `agentThreads`,
and `schedules` are the exceptions: they are looked up by the Google account,
the Notion user, the GitHub account, the Slack thread, or the time of a job, to
keep one account from being connected to two users, to keep one thread from
being answered for two users, and to let `robin schedule` find the jobs of
every user whose time has come. They hold only the owner's Slack IDs (and, for
`schedules`, the job ID and its next run time).

A conversation and its messages expire 30 days after the conversation started
(`[agent] session_ttl`). The conversation stores what the model read, including
the content of tool results such as mail bodies and file text.

## 4. Set up the Google Workspace integration (optional)

Skip this step to run Robin without Google Workspace; the settings page then
shows the integration as not available. Use a Google Cloud project that belongs
to your Google Workspace organization, signed in as a user of that
organization.

1. Enable the APIs. In the Google Cloud console, open **APIs & Services** →
   **Library** and enable **Google Calendar API**, **Google Drive API**, and
   **Gmail API**.
2. Configure the consent screen. Open **Google Auth platform** → **Branding**
   and enter the app name (for example `Robin`) and a user support email.
   On **Audience**, set the user type to **Internal**. This is required: with
   Internal, Google admits only accounts of your organization, and Robin does
   not check the account's domain itself. Internal apps do not need Google's
   verification for the sensitive and restricted scopes above.
3. Register the scopes (optional for Internal apps, but it documents what the
   app asks for). On **Data Access** → **Add or Remove Scopes**, add `openid`,
   `.../auth/userinfo.email`, `.../auth/calendar.readonly`,
   `.../auth/drive.readonly`, and `.../auth/gmail.readonly`.
4. Create the OAuth client. Open **Google Auth platform** → **Clients** →
   **Create Client**, choose the application type **Web application**, and
   under **Authorized redirect URIs** add
   `https://robin.example.com/api/v1/integrations/google-workspace/callback`,
   with your `ROBIN_BASE_URL` in place of `https://robin.example.com`. Click
   **Create** and copy:
   - **Client ID** → `ROBIN_GOOGLE_CLIENT_ID`
   - **Client secret** → `ROBIN_GOOGLE_CLIENT_SECRET` (store it right away; the
     console may not show it again)

   An OAuth client created before the API moved under `/api/v1` has
   `.../api/integrations/google-workspace/callback` as its redirect URI;
   replace it with the URI above, or Google rejects the connection.
5. Allow the app in the Google Admin console. If Gmail, Drive, or Calendar is
   set to **Restricted** under **Security** → **API controls**, Google refuses
   the grant until the app is trusted. Either select **Trust internal apps**
   under **API controls** → **Settings** → **Internal apps**, or open
   **Manage Third-Party App Access** → **Add app** → **OAuth App Name or
   Client ID**, search for the client ID, select it, and choose **Trusted**.
6. Start Robin with both values set (step 8). Users then connect their account
   with **Connect Google Workspace** on the settings page.

## 5. Set up the Notion integration (optional)

Skip this step to run Robin without Notion; the settings page then shows the
integration as not available. You need to be able to create integrations in
the Notion workspace that Robin should read.

1. Create a public integration. Open the Notion developer portal
   (https://developers.notion.com/), choose **Build** → **Public connections**
   in the sidebar, and click **Create new connection**.
2. Enter the name (for example `Robin`) and choose the development workspace.
3. Set the installation scope to **Selected workspaces only** and select your
   organization's workspace. This setting cannot be changed after the
   integration is created.
4. Under the capabilities, enable **Read content** only and leave every other
   capability off. Robin never writes to Notion.
5. Under the OAuth configuration, add the redirect URI
   `https://robin.example.com/api/v1/integrations/notion/callback`, with your
   `ROBIN_BASE_URL` in place of `https://robin.example.com`.
6. Create the integration, open its **Configuration** tab, and copy:
   - **OAuth client ID** → `ROBIN_NOTION_CLIENT_ID`
   - **OAuth client secret** → `ROBIN_NOTION_CLIENT_SECRET`
7. Set the ID of the workspace (a UUID such as
   `0f4a2b1c-3d4e-4f50-8a6b-7c8d9e0f1a2b`) → `ROBIN_NOTION_WORKSPACE_ID`.
   Notion's documentation does not say where the workspace ID is shown. If you
   do not know it, start Robin with any UUID, such as
   `00000000-0000-0000-0000-000000000000`, and connect Notion on the settings
   page: Robin rejects the connection as another workspace and logs the error
   `notion authorization is for another workspace` with the authorized
   `workspace_id`. Set that value and restart Robin.
8. Start Robin with the three values set (step 8). Users then connect Notion
   with **Connect Notion** on the settings page.

## 6. Set up the GitHub integration (optional)

Skip this step to run Robin without GitHub; the settings page then shows the
integration as not available. Create one GitHub App per deployment, owned by
the organization that uses Robin. You need to be an owner of the organization,
or an app manager of it.

1. Create the app. Open the URL below with `ORGANIZATION` replaced by the
   organization's login and `https://robin.example.com` by your
   `ROBIN_BASE_URL` (in both places). It fills in the settings and the
   permissions in the table below. The app name must be unique on GitHub;
   change `name` if it is taken.

   ```text
   https://github.com/organizations/ORGANIZATION/settings/apps/new?name=Robin-ORGANIZATION&url=https://robin.example.com&callback_urls[]=https://robin.example.com/api/v1/integrations/github/callback&request_oauth_on_install=false&public=false&webhook_active=false&actions=read&artifact_metadata=read&attestations=read&checks=read&code_quality=read&contents=read&deployments=read&discussions=read&environments=read&issues=read&merge_queues=read&metadata=read&packages=read&pages=read&pull_requests=read&repository_custom_properties=read&repository_projects=read&statuses=read&members=read&organization_projects=read&organization_packages=read&organization_events=read&custom_properties_for_organizations=read
   ```

   Check the page before clicking **Create GitHub App**:
   - **Callback URL** is `https://robin.example.com/api/v1/integrations/github/callback`.
   - **Expire user authorization tokens** is checked (the default). Robin
     refreshes the tokens itself.
   - **Request user authorization (OAuth) during installation** and **Enable
     Device Flow** are not checked.
   - **Webhook** → **Active** is not checked.
   - **Where can this GitHub App be installed?** is **Only on this account**.
2. Check the permissions. Every permission Robin needs is **Read-only**; the
   others stay **No access**.

   | Group | Read-only | No access, and why |
   | --- | --- | --- |
   | Repository | Actions, Artifact metadata, Attestations, Checks, Code quality, Commit statuses, Contents, Custom properties, Deployments, Discussions, Environments, Issues, Merge queues, Metadata, Packages, Pages, Projects, Pull requests | Secrets and Dependabot secrets (secret values and names); Secret scanning alerts, Code scanning alerts, and Dependabot alerts (security findings); Administration, Webhooks, and Codespaces (access control and infrastructure); Single file (covered by Contents); Workflows (write only) |
   | Organization | Members, Projects, Packages, Events, Custom properties for organizations | Secrets; Administration, Webhooks, Self-hosted runners, Personal access tokens and their requests, Blocking users, Custom repository roles, Custom organization roles, Custom properties management, Copilot settings, Announcement banners, and Plan (access control, infrastructure, and organization settings) |
   | Account | none | All: they cover the user's personal settings, and Robin reads the connected account with `GET /user`, which needs none |

   To give Robin access to more, change the app's permissions later; Robin
   needs no change, and users approve the new permissions the next time they
   connect.
3. Generate the client secret. On the app's **General** page, copy the
   **Client ID** (it starts with `Iv`) → `ROBIN_GITHUB_CLIENT_ID`, click
   **Generate a new client secret**, and copy it → `ROBIN_GITHUB_CLIENT_SECRET`
   (GitHub shows it only once). Robin does not use a private key; do not
   generate one.
4. Install the app on the organization. On **Install App**, click **Install**
   next to the organization and choose **All repositories**. With **Only
   select repositories**, Robin cannot read the other repositories even for
   users who can.
5. Start Robin with both values set (step 8). Users then connect their account
   with **Connect GitHub** on the settings page.

## 7. Set up Claude

Robin answers mentions and writes scheduled messages with Claude, through
Vertex AI or the Claude API. `robin serve` needs it whenever Slack events are
enabled (the bot token and the signing secret are set), and `robin schedule`
always needs it; set exactly one of the two options below.

- **Vertex AI**: in the Google Cloud project that runs Robin, enable the
  Vertex AI API, enable the Claude model (Claude Sonnet 5.5 by default) in
  **Vertex AI** → **Model Garden**, and grant the service account that runs
  Robin `roles/aiplatform.user`. Set the project →
  `ROBIN_LLM_VERTEX_PROJECT_ID`, and the region (default `global`) →
  `ROBIN_LLM_VERTEX_REGION`. Robin authenticates with Application Default
  Credentials.
- **Claude API**: create an API key in the Claude Console and pass it as
  `ROBIN_ANTHROPIC_API_KEY`, for example from Secret Manager.

The model, its prices, the cost limit of one mention, and how long a
conversation is kept are set in a TOML file given with `--config`
(`ROBIN_CONFIG`). Without the file, every value takes the default below.

```toml
[llm]
provider = "claude"                # only "claude" is supported
model = "claude-sonnet-5-5"
input_usd_per_mtok = 2.0           # dollars per million input tokens
output_usd_per_mtok = 10.0
cache_read_usd_per_mtok = 0.2
cache_write_usd_per_mtok = 2.5     # writes to the 5-minute prompt cache

[agent]
budget_usd = 2.0                   # budget of one mention
session_ttl = "720h"               # a conversation ends this long after it started
```

| Key | Default | Rule |
| --- | --- | --- |
| `llm.provider` | `claude` | Only `claude`. Writing it requires `llm.model` and the four prices |
| `llm.model` | `claude-sonnet-5-5` | Writing it requires the four prices |
| `llm.*_usd_per_mtok` | `2.0`, `10.0`, `0.2`, `2.5` | Written only together with `llm.model`, all four. Input and output are positive, the cache prices are zero or more |
| `agent.budget_usd` | `2.0` | Positive |
| `agent.session_ttl` | `720h` | A positive Go duration such as `168h` |

The defaults are the prices of Claude Sonnet 5.5 on the Claude API. Robin
measures the cost of each mention with these prices, so write the prices that
you actually pay, such as the Vertex AI prices of the model, when they differ.
An unknown key, a value out of range, or a file that cannot be read stops the
server at startup.

## 8. Run the server

```sh
robin serve
```

| Flag | Environment variable | Default | Required | Description |
| --- | --- | --- | --- | --- |
| `--config` | `ROBIN_CONFIG` | | | TOML settings file of the model, prices, cost limit, and conversation lifetime (step 7) |
| `--addr` | `ROBIN_ADDR` | `:8080` | | Listen address |
| `--base-url` | `ROBIN_BASE_URL` | | yes | Public URL, `scheme://host[:port]`. Used for the OAuth callback, the link in login prompts, and the `Secure` cookie attribute (`https` only) |
| `--session-ttl` | `ROBIN_SESSION_TTL` | `168h` | | Lifetime of a web session |
| `--log-level` | `ROBIN_LOG_LEVEL` | `info` | | `debug`, `info`, `warn`, `error` |
| `--log-format` | `ROBIN_LOG_FORMAT` | `console` | | `console`, `json` |
| `--repository-backend` | `ROBIN_REPOSITORY_BACKEND` | `firestore` | | `firestore`, or `memory` for local development (single process only) |
| `--firestore-project-id` | `ROBIN_FIRESTORE_PROJECT_ID` | | with `firestore` | Google Cloud project of Firestore |
| `--firestore-database-id` | `ROBIN_FIRESTORE_DATABASE_ID` | `(default)` | | Firestore database ID |
| `--slack-client-id` | `ROBIN_SLACK_CLIENT_ID` | | yes (not with `--no-auth`) | Client ID of the Slack app |
| `--slack-client-secret` | `ROBIN_SLACK_CLIENT_SECRET` | | yes (not with `--no-auth`) | Client secret of the Slack app |
| `--slack-signing-secret` | `ROBIN_SLACK_SIGNING_SECRET` | | yes (with `--no-auth`: together with the bot token, or neither) | Signing secret, used to verify Events API requests |
| `--slack-bot-token` | `ROBIN_SLACK_BOT_TOKEN` | | yes (with `--no-auth`: together with the signing secret, or neither) | Bot user OAuth token (`xoxb-`) |
| `--slack-team-id` | `ROBIN_SLACK_TEAM_ID` | | yes | The only workspace Robin accepts sign-ins and events from |
| `--slack-api-url` | `ROBIN_SLACK_API_URL` | `https://slack.com/api/` | | Development and E2E only. Base URL of the Slack Web API the bot calls; any other value is accepted only with `--no-auth` |
| `--llm-vertex-project-id` | `ROBIN_LLM_VERTEX_PROJECT_ID` | | one of this and `--anthropic-api-key` when Slack events are enabled | Google Cloud project to call Claude through Vertex AI (step 7) |
| `--llm-vertex-region` | `ROBIN_LLM_VERTEX_REGION` | `global` | | Vertex AI region of Claude |
| `--anthropic-api-key` | `ROBIN_ANTHROPIC_API_KEY` | | one of this and `--llm-vertex-project-id` when Slack events are enabled | API key to call the Claude API directly |
| `--kms-key-name` | `ROBIN_KMS_KEY_NAME` | | yes (not with `--no-auth`) | Cloud KMS key for the Slack, Google, Notion, and GitHub user tokens |
| `--google-client-id` | `ROBIN_GOOGLE_CLIENT_ID` | | with `--google-client-secret` | Client ID of the Google OAuth client (step 4). Setting both Google values enables the Google Workspace integration |
| `--google-client-secret` | `ROBIN_GOOGLE_CLIENT_SECRET` | | with `--google-client-id` | Client secret of the same OAuth client |
| `--notion-client-id` | `ROBIN_NOTION_CLIENT_ID` | | with the other two Notion values | OAuth client ID of the Notion public integration (step 5). Setting the three Notion values enables the Notion integration |
| `--notion-client-secret` | `ROBIN_NOTION_CLIENT_SECRET` | | with the other two Notion values | OAuth client secret of the same integration |
| `--notion-workspace-id` | `ROBIN_NOTION_WORKSPACE_ID` | | with the other two Notion values | ID (UUID) of the only Notion workspace users can connect |
| `--notion-api-url` | `ROBIN_NOTION_API_URL` | `https://api.notion.com` | | Development and E2E only. Origin of the Notion API; any other value is accepted only with `--no-auth` |
| `--github-client-id` | `ROBIN_GITHUB_CLIENT_ID` | | with `--github-client-secret` | Client ID of the GitHub App (step 6, starts with `Iv`). Setting both GitHub values enables the GitHub integration |
| `--github-client-secret` | `ROBIN_GITHUB_CLIENT_SECRET` | | with `--github-client-id` | Client secret of the same GitHub App |
| `--no-auth` | `ROBIN_NO_AUTH` | | | Development and E2E only. A Slack user ID (`U...`) of `--slack-team-id`: every web sign-in becomes this user without asking Slack, and no Slack user token is stored. Accepted only with `--repository-backend memory` |

With `--no-auth`, the Slack endpoints (`/hooks/slack/event` and
`/hooks/slack/interaction`) exist only when both the bot token and the signing
secret are set. Without
`--kms-key-name`, tokens are encrypted with a key that the server generates at
startup and loses when it stops; the in-memory repository loses the tokens at
the same time.

The API of the server is under `/api/v1`. Paths without the version, such as
`/api/auth/login`, return `404`.

Google Cloud credentials are read from Application Default Credentials.

### Deployment notes

- Robin can run as several instances; state shared between requests is kept in
  Firestore.
- Slack requires a response within three seconds, so Robin acknowledges an
  event first and handles it in the background of the same process. One
  answer can run for up to 10 minutes. On Cloud Run, set CPU to be always
  allocated; with CPU allocated only during requests, the background work is
  throttled after the response and replies are delayed or lost. An instance
  that stops in the middle of an answer leaves the conversation locked for 11
  minutes; the next mention after that continues it.
- Put TLS in front of the server and use an `https` base URL, so the session
  cookies carry the `Secure` attribute.

## 9. Run the scheduler

```sh
robin schedule
```

`robin schedule` posts the scheduled messages whose time has come and exits
([How Robin posts scheduled messages](#how-robin-posts-scheduled-messages)).
Start it every few minutes with a scheduler of your choice, with the same
Firestore, Slack bot token, Claude settings, and settings file as `robin
serve`; for example a Cloud Run job started by Cloud Scheduler, or cron:

```cron
*/5 * * * * robin schedule
```

It takes these flags and no others; the shared ones mean the same as for
`robin serve` (section 8):

| Flag | Environment variable | Default | Required | Description |
| --- | --- | --- | --- | --- |
| `--config` | `ROBIN_CONFIG` | | | Settings file (step 7). The model and prices of `[llm]` are used for the greeting |
| `--repository-backend` | `ROBIN_REPOSITORY_BACKEND` | `firestore` | | Only `firestore`: the in-memory repository of this process holds no jobs |
| `--firestore-project-id` | `ROBIN_FIRESTORE_PROJECT_ID` | | yes | Google Cloud project of Firestore |
| `--firestore-database-id` | `ROBIN_FIRESTORE_DATABASE_ID` | `(default)` | | Firestore database ID |
| `--slack-bot-token` | `ROBIN_SLACK_BOT_TOKEN` | | yes | Bot user OAuth token, used to post the messages |
| `--slack-api-url` | `ROBIN_SLACK_API_URL` | `https://slack.com/api/` | | Must stay at the default |
| `--llm-vertex-project-id` | `ROBIN_LLM_VERTEX_PROJECT_ID` | | one of this and `--anthropic-api-key` | Vertex AI project of Claude |
| `--llm-vertex-region` | `ROBIN_LLM_VERTEX_REGION` | `global` | | Vertex AI region of Claude |
| `--anthropic-api-key` | `ROBIN_ANTHROPIC_API_KEY` | | one of this and `--llm-vertex-project-id` | API key of the Claude API |
| `--max-delay` | `ROBIN_SCHEDULE_MAX_DELAY` | `1h` | | A message is skipped when the command runs this long or longer after its time |
| `--concurrency` | `ROBIN_SCHEDULE_CONCURRENCY` | `4` | | Number of messages written and posted at the same time |

The service account needs `roles/datastore.user` and, with Vertex AI,
`roles/aiplatform.user`; it does not use Cloud KMS. One message is written
within 2 minutes; the command exits once every message it started has ended.
On SIGINT or SIGTERM it starts no more messages, waits for those it started,
and exits with a non-zero status. It also exits with a non-zero status when
it cannot read the jobs from Firestore. Each run is logged with its result
and cost (`job run finished`), and the command ends with the counts
(`scheduled jobs finished`).

## Local development

- Backend with Slack: `robin serve --repository-backend memory ...` with the
  Slack and KMS settings. The Slack app needs a public URL for the OAuth
  redirect and events; use a tunnel and set `--base-url` to it.
- Backend without Slack: `robin serve --base-url http://localhost:8080
  --repository-backend memory --slack-team-id T0123ABCD --no-auth U0123ABCD`.
  "Sign in with Slack" signs you in as `U0123ABCD` directly.
- Google Workspace in local development: `--no-auth` does not skip Google. With
  the Google flags set, **Connect Google Workspace** goes to the real Google
  authorization. Without `--kms-key-name` the token is encrypted with the
  temporary key described above. The redirect URI registered in the OAuth
  client must match `--base-url`.
- Notion in local development: with the Notion flags set, **Connect Notion**
  goes to the real Notion authorization, and the redirect URI of the
  integration must match `--base-url`. Alternatively, run the fake Notion
  server of the E2E tests (`node e2e/fake-notion.mjs` in `frontend/`, with
  `FAKE_NOTION_CLIENT_ID`, `FAKE_NOTION_CLIENT_SECRET`, and
  `FAKE_NOTION_WORKSPACE_ID` matching the Notion flags) and add
  `--notion-api-url http://127.0.0.1:18082` to a `--no-auth` server.
- GitHub in local development: with the GitHub flags set, **Connect GitHub**
  goes to the real GitHub authorization, and the callback URL of the app must
  match `--base-url`. Without `--kms-key-name` the tokens are encrypted with
  the temporary key described above.
- Scheduled messages in local development: `robin schedule` reads the jobs
  from Firestore, so it cannot run against a `--repository-backend memory`
  server. Run both commands against the Firestore emulator
  (`FIRESTORE_EMULATOR_HOST=127.0.0.1:28615`, started as in
  `task test:firestore`) with the same `--firestore-project-id`.
- Frontend: `task dev:frontend` starts Vite on port 5173 and forwards `/api` to
  `http://localhost:8080`.
- Build the frontend before building the binary: `task build:frontend`. The
  output in `frontend/dist` is embedded into the Go binary.

### Tests

- `go test ./...` runs every Go test. The repository tests run against both the
  in-memory backend and Firestore. The Firestore side connects to an emulator on
  `127.0.0.1:28615` (start it with `task test:firestore`, which needs Docker), or
  to a real project when `TEST_FIRESTORE_PROJECT_ID` is set.
- Tests that use a real Cloud KMS key run only when `TEST_GCP_KMS` is set to a
  key name (`projects/*/locations/*/keyRings/*/cryptoKeys/*`), for example with
  `zenv go test ./...`.
- Frontend: `pnpm test`, `pnpm lint`, and `pnpm build` in `frontend/`.
- E2E: `task e2e` builds the binary and runs the Playwright tests in
  `frontend/e2e/tests/` against it, started with `--no-auth` and the in-memory
  repository. Notion is served by `frontend/e2e/fake-notion.mjs` and the Slack
  Web API of the bot by `frontend/e2e/fake-slack.mjs`, which Playwright starts
  together with the server. Install the browser once with
  `pnpm exec playwright install chromium` in `frontend/`.
- Screenshots for pull requests: `task screenshots` captures every screen state
  into `frontend/screenshots/`. Attach them to the PR description with
  `gh pr edit <number> --body-file <body.md> --attach '<file>#<alt text>' ...`;
  a body reference to the same path, such as
  `![Login: failed](./frontend/screenshots/login-failed.png)`, is rewritten to
  the uploaded image. Do not commit them or push them to any branch.
