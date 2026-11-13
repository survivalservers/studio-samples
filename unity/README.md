# Survival Servers Studio for Unity

A dedicated game server made with Unity, with no SDK to add. It reads its ports and match settings from the
environment, and `LocalApi` makes every optional call to the local API (ready, players, labels, stop) under the
same names as our game server SDK, through Unity's non-blocking `UnityWebRequest`. Without `GS_LOCAL_API_URL` (a run
in the editor or on your own PC) every call does nothing.

- `Runtime/Core`: the game port, the match rules and the local API calls, in plain C# with no UnityEngine types.
- `Runtime/Unity`: `GameServerBehaviour`, the one component that runs them every frame.
- `Editor`: dedicated server builds (menu Game Server).

Add it by path: Window, Package Manager, +, "Add package from disk", then pick this folder's `package.json`. It needs
Unity 2021.3 or newer and brings `com.unity.nuget.newtonsoft-json`.

Guide: https://studio.survivalservers.com/docs/get-started/unity/

Licence: Apache 2.0 (`LICENSE.md`, with `NOTICE.md`).
