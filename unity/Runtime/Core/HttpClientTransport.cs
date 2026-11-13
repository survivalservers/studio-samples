using System;
using System.Net.Http;
using System.Net.Http.Headers;
using System.Text;
using System.Threading;
using System.Threading.Tasks;

namespace GameServerSample.Core
{
	/// <summary>The local API over HttpClient. Works in Unity server builds too; the sample's default there is UnityWebRequest.</summary>
	public sealed class HttpClientTransport : ILocalTransport, IDisposable
	{
		private readonly HttpClient _http;
		private readonly string _baseUrl;

		public HttpClientTransport(string agentUrl, string token, HttpMessageHandler handler = null)
		{
			_baseUrl = agentUrl.TrimEnd('/');
			_http = handler == null ? new HttpClient() : new HttpClient(handler);
			_http.Timeout = LocalApi.TransportTimeout;
			_http.DefaultRequestHeaders.Authorization = new AuthenticationHeaderValue("Bearer", token);
		}

		public async Task<LocalReply> Send(string method, string path, string json, CancellationToken ct)
		{
			try
			{
				using (var req = new HttpRequestMessage(new HttpMethod(method), _baseUrl + path))
				{
					if (json != null) req.Content = new StringContent(json, Encoding.UTF8, "application/json");
					using (var res = await _http.SendAsync(req, ct))
						return new LocalReply((int)res.StatusCode, await res.Content.ReadAsStringAsync());
				}
			}
			catch (HttpRequestException) { return new LocalReply(0, ""); }
			catch (TaskCanceledException) when (!ct.IsCancellationRequested) { return new LocalReply(0, ""); }
		}

		/// <summary>How long a call waits for its answer (<see cref="LocalApi.TransportTimeout"/>).</summary>
		public TimeSpan Timeout => _http.Timeout;

		public void Dispose() => _http.Dispose();
	}
}
