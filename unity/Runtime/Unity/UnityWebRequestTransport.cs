using System.Text;
using System.Threading;
using System.Threading.Tasks;
using GameServerSample.Core;
using UnityEngine.Networking;

namespace GameServerSample
{
	/// <summary>
	/// The local API over Unity's own UnityWebRequest: no plugin and no native library, so it keeps working
	/// across Unity upgrades and under IL2CPP. Call it from the main thread, as Unity requires.
	/// </summary>
	public sealed class UnityWebRequestTransport : ILocalTransport
	{
		private readonly string _baseUrl;
		private readonly string _token;

		public UnityWebRequestTransport(string agentUrl, string token)
		{
			_baseUrl = agentUrl.TrimEnd('/');
			_token = token;
		}

		public async Task<LocalReply> Send(string method, string path, string json, CancellationToken ct)
		{
			using (var req = new UnityWebRequest(_baseUrl + path, method))
			{
				req.downloadHandler = new DownloadHandlerBuffer();
				if (json != null)
				{
					req.uploadHandler = new UploadHandlerRaw(Encoding.UTF8.GetBytes(json));
					req.SetRequestHeader("Content-Type", "application/json");
				}
				req.SetRequestHeader("Authorization", "Bearer " + _token);
				req.timeout = (int)LocalApi.TransportTimeout.TotalSeconds; // seconds

				var op = req.SendWebRequest();
				while (!op.isDone)
				{
					if (ct.IsCancellationRequested)
					{
						req.Abort();
						ct.ThrowIfCancellationRequested();
					}
					await Task.Yield(); // continues on the next frame, on the main thread
				}
				// responseCode is 0 when the call did not complete (no connection, timeout).
				return new LocalReply((int)req.responseCode, req.downloadHandler.text);
			}
		}
	}
}
