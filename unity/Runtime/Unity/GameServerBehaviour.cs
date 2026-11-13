using System;
using System.Threading;
using System.Threading.Tasks;
using GameServerSample.Core;
using UnityEngine;

namespace GameServerSample
{
	/// <summary>
	/// Add this to an object in your server scene. In a dedicated server build it binds the game port from GS_* variables,
	/// says ready, waits for its match, reports players and stops when the match ends. In the editor and in client builds
	/// it does nothing unless <see cref="runInEditor"/> is set.
	/// </summary>
	public sealed class GameServerBehaviour : MonoBehaviour
	{
		[Tooltip("Also run in the editor's play mode (as a local server on port 7777).")]
		public bool runInEditor;

		[Tooltip("End the match after this many seconds; 0 ends it when everyone who joined has left.")]
		public float matchLengthSeconds;

		private readonly CancellationTokenSource _cts = new CancellationTokenSource();
		private MatchServer _server;
		private bool _finishing;

		private async void Start()
		{
			if (!IsDedicatedServer() && !(Application.isEditor && runInEditor))
			{
				enabled = false;
				return;
			}
			Application.targetFrameRate = 60; // a server has no screen to keep up with
			Application.quitting += OnQuitting;

			try
			{
				var cfg = GsConfig.FromEnvironment();
				var api = new LocalApi(cfg.AgentUrl.Length > 0 ? new UnityWebRequestTransport(cfg.AgentUrl, cfg.Token) : null);
				_server = new MatchServer(cfg, api, Debug.Log, matchLengthSeconds > 0 ? TimeSpan.FromSeconds(matchLengthSeconds) : (TimeSpan?)null);
				await _server.StartAsync(_cts.Token);
			}
			catch (Exception e)
			{
				// Exiting with an error marks the start as failed, with this line in the log.
				Debug.LogError("game server start failed: " + e);
				Application.Quit(1);
			}
		}

		private void Update()
		{
			if (_server == null || _finishing) return;
			_server.Tick();
			if (_server.EndReason != null)
			{
				_finishing = true;
				_ = FinishAndQuit();
			}
		}

		private async Task FinishAndQuit()
		{
			try { await _server.FinishAsync(_cts.Token); }
			catch (Exception e) { Debug.LogError("finishing the match: " + e.Message); }
			Application.Quit(0);
		}

		// The platform's stop signal (Ctrl+Break on Windows, SIGTERM on Linux) quits the player; save what you must here,
		// it cannot wait for the network. Versions can instead stop through the local API ("stop": {"method":
		// "local_api"}), which the server waits for and finishes properly.
		private void OnQuitting()
		{
			if (_server != null && !_finishing) _server.FinishNow("stop_signal");
			_cts.Cancel();
			_server?.Dispose();
		}

		private static bool IsDedicatedServer()
		{
#if UNITY_SERVER
			return true;
#else
			return Application.isBatchMode && !Application.isEditor;
#endif
		}
	}
}
