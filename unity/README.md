# Survival Servers Studio for Unity

The calls a dedicated game server made with Unity needs, with no SDK to add. `GsConfig` reads the ports and match
settings from the environment, and `LocalApi` makes every optional call to the local API (ready, players, labels,
stop) under the same names as our game server SDK, through Unity's non-blocking `UnityWebRequest`. Without
`GS_LOCAL_API_URL` (a run in the editor or on your own PC) every call does nothing.

- `Runtime/Core`: `GsConfig`, `LocalApi` and `HttpClientTransport`, in plain C# with no UnityEngine types.
- `Runtime/Unity`: `UnityWebRequestTransport`, the calls through `UnityWebRequest`.
- `Editor`: dedicated server builds (menu Survival Servers Studio).
- `Samples~/SampleGame`: a small UDP game server that uses them, with `GameServerBehaviour`, the one component that
  runs it every frame. Import it from the package's Samples in the Package Manager.

Assemblies and namespaces: `SurvivalServers.Unity` and `SurvivalServers.Unity.Editor`; the sample is
`GameServerSample`.

Add it by path: Window, Package Manager, +, "Add package from disk", then pick this folder's `package.json`. It needs
Unity 2021.3 or newer and brings `com.unity.nuget.newtonsoft-json`.

Guide: https://studio.survivalservers.com/docs/get-started/unity/

Licence: Apache 2.0 (`LICENSE.md`, with `NOTICE.md`).
