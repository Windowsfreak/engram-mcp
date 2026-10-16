import json
import os
import shutil
import subprocess
import sys
import time
import requests
import statistics

SERVER_URL = "http://127.0.0.1:8001"

def wait_for_server(timeout=600):
    print("Waiting for sidecar model to initialize (downloading weights if first run)...")
    start = time.time()
    last_print = 0
    while time.time() - start < timeout:
        try:
            resp = requests.get(f"{SERVER_URL}/health", timeout=1)
            if resp.status_code == 200:
                data = resp.json()
                print(f"Server is ready! Model: {data.get('model')}, Device: {data.get('device')}")
                return True
        except Exception:
            pass
        if time.time() - last_print > 15:
            last_print = time.time()
            elapsed = int(time.time() - start)
            print(f"  ... still waiting for server startup ({elapsed}s elapsed) ...")
        time.sleep(1.0)
    return False

def ensure_server():
    try:
        r = requests.get(f"{SERVER_URL}/health", timeout=1)
        if r.status_code == 200:
            print("Sidecar server is already running on port 8001.")
            return None
    except Exception:
        pass

    print("Launching Uvicorn sidecar subprocess...")
    uv_bin = shutil.which("uv") or "uv"
    proc = subprocess.Popen([
        uv_bin, "run", "--python", "3.12",
        "--with", "fastapi", "--with", "uvicorn[standard]", "--with", "sentence-transformers", "--with", "transformers<5.0.0", "--with", "torch", "--with", "einops",
        "python", "sidecar/main.py"
    ])
    if not wait_for_server(timeout=600):
        proc.terminate()
        proc.wait()
        raise RuntimeError("Timed out waiting for sidecar to start after 600 seconds.")
    return proc

def run_benchmark():
    # Warmup
    print("\nWarming up model...")
    for _ in range(3):
        requests.post(f"{SERVER_URL}/embed", json={
            "input": ["search_query: machine learning optimization benchmark"],
            "model": "nomic-ai/nomic-embed-text-v2-moe"
        })

    # 1. Single Query Latency Test (Simulating Search / Retrieval)
    print("\n--- 1. Single Query Latency Test (10 requests) ---")
    query_times = []
    queries = [
        "search_query: Kalman filter state prediction covariance matrix",
        "search_query: recursive Bayesian estimation dynamic system",
        "search_query: Cholesky decomposition positive definite matrix",
        "search_query: hybrid full text lexical and semantic vector search",
        "search_query: postgres pgvector HNSW cosine distance indexing",
        "search_query: agent durable long term episodic memory logging",
        "search_query: negative knowledge graveyard guardrail post mortem",
        "search_query: reciprocal rank fusion scoring algorithm",
        "search_query: subword tokenization sentence piece boundary",
        "search_query: low latency fastapi uvicorn microservice embedding"
    ]

    for q in queries:
        t0 = time.perf_counter()
        resp = requests.post(f"{SERVER_URL}/embed", json={
            "input": [q],
            "model": "nomic-ai/nomic-embed-text-v2-moe"
        })
        t1 = time.perf_counter()
        elapsed_ms = (t1 - t0) * 1000
        if resp.status_code == 200:
            query_times.append(elapsed_ms)
            print(f"  Query latency: {elapsed_ms:6.2f} ms")
        else:
            print(f"  Error: {resp.status_code} {resp.text}")

    # 2. Batch Chunk Latency Test (Simulating Document Ingestion)
    batch_sizes = [4, 8, 16, 32]
    sample_chunk = (
        "search_document: The Kalman filter uses a system dynamics model, known control inputs to that system, "
        "and multiple sequential measurements from sensors to form an estimate of the system varying quantities "
        "that is better than the estimate obtained by using any one measurement alone. All measurements and "
        "calculations based on models are estimates to some degree."
    )

    batch_results = []
    print("\n--- 2. Batch Ingestion Latency Test ---")
    for bs in batch_sizes:
        batch_inputs = [sample_chunk] * bs
        times = []
        for _ in range(5):
            t0 = time.perf_counter()
            resp = requests.post(f"{SERVER_URL}/embed", json={
                "input": batch_inputs,
                "model": "nomic-ai/nomic-embed-text-v2-moe"
            })
            t1 = time.perf_counter()
            if resp.status_code == 200:
                times.append((t1 - t0) * 1000)

        avg_ms = statistics.mean(times)
        per_chunk_ms = avg_ms / bs
        throughput = (bs / (avg_ms / 1000.0))
        batch_results.append((bs, avg_ms, per_chunk_ms, throughput))
        print(f"  Batch size {bs:2d}: {avg_ms:6.2f} ms total | {per_chunk_ms:5.2f} ms/chunk | {throughput:6.1f} chunks/sec")

    # Summary
    print("\n================ BENCHMARK SUMMARY ================")
    print(f"Single Query Latency:")
    print(f"  Mean:   {statistics.mean(query_times):6.2f} ms")
    print(f"  Median: {statistics.median(query_times):6.2f} ms")
    print(f"  Min:    {min(query_times):6.2f} ms")
    print(f"  Max:    {max(query_times):6.2f} ms")
    print(f"Batch Ingestion Throughput:")
    for bs, total_ms, per_chunk, tps in batch_results:
        print(f"  Batch of {bs:2d}: {total_ms:6.2f} ms ({per_chunk:5.2f} ms/chunk) -> {tps:5.1f} chunks/sec")
    print("===================================================\n")

if __name__ == "__main__":
    proc = ensure_server()
    try:
        run_benchmark()
    finally:
        if proc is not None:
            print("\nShutting down sidecar...")
            proc.terminate()
            proc.wait()
            print("Sidecar stopped.")
