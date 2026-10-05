#!/usr/bin/env python3
"""
Automated Benchmarking Suite for HAProxy SPOA:
Compares HAProxy performance BEFORE and AFTER enabling haproxy-metrics-spoa.

Arms:
1. Baseline (No SPOA): HAProxy frontend without SPOE filter (port 18082)
2. SPOA UNIX Socket: HAProxy frontend with SPOE async filter via UNIX domain socket (port 18081)
3. SPOA TCP Socket: HAProxy frontend with SPOE async filter via TCP socket (port 18080)
"""

import sys
import os
import subprocess
import re
import json
import time
import statistics
import urllib.request

ENDPOINTS = {
    "Baseline (No SPOA)": "http://127.0.0.1:18082/",
    "SPOA (UNIX Socket)": "http://127.0.0.1:18081/",
    "SPOA (TCP Socket)":  "http://127.0.0.1:18080/",
}

METRICS_ENDPOINTS = {
    "SPOA (UNIX Socket)": "http://127.0.0.1:19101/metrics",
    "SPOA (TCP Socket)":  "http://127.0.0.1:19100/metrics",
}

TIERS = [
    {"name": "Tier 1: Low Concurrency (10 conn, 4 threads, 10s)", "connections": 10, "threads": 4, "duration": 10},
    {"name": "Tier 2: Moderate Concurrency (50 conn, 4 threads, 15s)", "connections": 50, "threads": 4, "duration": 15},
    {"name": "Tier 3: High Concurrency (100 conn, 4 threads, 15s)", "connections": 100, "threads": 4, "duration": 15},
    {"name": "Tier 4: Stress Concurrency (200 conn, 4 threads, 15s)", "connections": 200, "threads": 4, "duration": 15},
]

RUNS_PER_TIER = 3
WARMUP_SECONDS = 3

def parse_time_us(s):
    """Convert time string (e.g., '471.00us', '1.70ms', '1.05s') to microseconds float."""
    s = s.strip()
    if s.endswith("us"):
        return float(s[:-2])
    elif s.endswith("ms"):
        return float(s[:-2]) * 1000.0
    elif s.endswith("s"):
        return float(s[:-1]) * 1000000.0
    elif s.endswith("m"):
        return float(s[:-1]) * 60000000.0
    return float(s)

def run_wrk(url, threads, connections, duration):
    cmd = [
        "wrk",
        "-t", str(threads),
        "-c", str(connections),
        "-d", f"{duration}s",
        "--latency",
        "-H", "Host: benchmark.local",
        url
    ]
    res = subprocess.run(cmd, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
    if res.returncode != 0:
        raise RuntimeError(f"wrk failed: {res.stderr}")
    return parse_wrk_output(res.stdout)

def parse_wrk_output(output):
    result = {}
    
    # Requests/sec
    rps_match = re.search(r"Requests/sec:\s+([\d\.]+)", output)
    result["rps"] = float(rps_match.group(1)) if rps_match else 0.0
    
    # Transfer/sec
    tps_match = re.search(r"Transfer/sec:\s+([\d\.]+)([KMG]B)", output)
    if tps_match:
        val = float(tps_match.group(1))
        unit = tps_match.group(2)
        multiplier = {"KB": 1024, "MB": 1024*1024, "GB": 1024*1024*1024}.get(unit, 1)
        result["transfer_bytes_sec"] = val * multiplier
    else:
        result["transfer_bytes_sec"] = 0.0

    # Total requests
    total_req_match = re.search(r"(\d+)\s+requests in", output)
    result["total_requests"] = int(total_req_match.group(1)) if total_req_match else 0

    # Thread Stats Latency (Avg, Max)
    # Thread Stats   Avg      Stdev     Max   +/- Stdev
    #   Latency     1.70ms    6.35ms  79.11ms   96.54%
    lat_row_match = re.search(r"Latency\s+([\d\.]+[a-z]+)\s+([\d\.]+[a-z]+)\s+([\d\.]+[a-z]+)", output)
    if lat_row_match:
        result["avg_latency_us"] = parse_time_us(lat_row_match.group(1))
        result["max_latency_us"] = parse_time_us(lat_row_match.group(3))
    else:
        result["avg_latency_us"] = 0.0
        result["max_latency_us"] = 0.0

    # Percentiles
    # 50%  471.00us
    # 75%  700.00us
    # 90%    1.44ms
    # 99%   40.59ms
    p50 = re.search(r"50%\s+([\d\.]+[a-z]+)", output)
    p75 = re.search(r"75%\s+([\d\.]+[a-z]+)", output)
    p90 = re.search(r"90%\s+([\d\.]+[a-z]+)", output)
    p99 = re.search(r"99%\s+([\d\.]+[a-z]+)", output)
    
    result["p50_us"] = parse_time_us(p50.group(1)) if p50 else 0.0
    result["p75_us"] = parse_time_us(p75.group(1)) if p75 else 0.0
    result["p90_us"] = parse_time_us(p90.group(1)) if p90 else 0.0
    result["p99_us"] = parse_time_us(p99.group(1)) if p99 else 0.0

    # Errors
    # Socket errors: connect 0, read 0, write 0, timeout 0
    err_match = re.search(r"Socket errors:\s+connect\s+(\d+),\s+read\s+(\d+),\s+write\s+(\d+),\s+timeout\s+(\d+)", output)
    if err_match:
        result["errors"] = {
            "connect": int(err_match.group(1)),
            "read": int(err_match.group(2)),
            "write": int(err_match.group(3)),
            "timeout": int(err_match.group(4)),
        }
    else:
        result["errors"] = {"connect": 0, "read": 0, "write": 0, "timeout": 0}

    return result

def get_container_stats():
    """Returns dict of container CPU % and Mem usage."""
    cmd = ["docker", "stats", "--no-stream", "--format", "{{.Name}}\t{{.CPUPerc}}\t{{.MemUsage}}"]
    res = subprocess.run(cmd, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
    stats = {}
    if res.returncode == 0:
        for line in res.stdout.strip().split("\n"):
            parts = line.split("\t")
            if len(parts) >= 3:
                name, cpu, mem = parts[0], parts[1], parts[2]
                stats[name] = {"cpu": cpu, "mem": mem}
    return stats

def get_spoa_messages_count(url):
    try:
        with urllib.request.urlopen(url, timeout=3) as resp:
            content = resp.read().decode('utf-8')
            # Look for haproxy_spoa_messages_received_total{status="ok"} 12345
            match = re.search(r'haproxy_spoa_messages_received_total\{status="ok"\}\s+([\d\.]+)', content)
            if match:
                return float(match.group(1))
    except Exception as e:
        pass
    return 0.0

def main():
    print("=" * 80)
    print("HAProxy Metrics SPOA — Comprehensive Performance Benchmark")
    print("=" * 80)
    
    # Ensure all endpoints are ready
    print("\n[+] Checking readiness of endpoints...")
    for arm, url in ENDPOINTS.items():
        try:
            with urllib.request.urlopen(url, timeout=3) as resp:
                print(f"  ✓ {arm}: {url} -> HTTP {resp.status}")
        except Exception as e:
            print(f"  ✗ {arm}: {url} FAILED: {e}")
            sys.exit(1)

    all_results = {}

    for tier in TIERS:
        tier_name = tier["name"]
        conn = tier["connections"]
        threads = tier["threads"]
        duration = tier["duration"]
        
        print("\n" + "=" * 80)
        print(f"Executing: {tier_name}")
        print("=" * 80)

        all_results[tier_name] = {}

        for arm, url in ENDPOINTS.items():
            print(f"\n---> Testing Arm: {arm} ({url})")
            
            # Warm-up
            print(f"  [Warmup] Sending {WARMUP_SECONDS}s warmup load...")
            run_wrk(url, threads, conn, WARMUP_SECONDS)
            time.sleep(1)

            runs = []
            for r in range(RUNS_PER_TIER):
                print(f"  [Run {r+1}/{RUNS_PER_TIER}] Testing ({duration}s, {conn} connections)...", end="", flush=True)
                metrics_before = get_spoa_messages_count(METRICS_ENDPOINTS.get(arm, "")) if arm in METRICS_ENDPOINTS else 0
                
                stats_before = get_container_stats()
                start_t = time.time()
                data = run_wrk(url, threads, conn, duration)
                elapsed = time.time() - start_t
                stats_after = get_container_stats()
                
                metrics_after = get_spoa_messages_count(METRICS_ENDPOINTS.get(arm, "")) if arm in METRICS_ENDPOINTS else 0
                msg_delta = metrics_after - metrics_before
                
                data["msg_delta"] = msg_delta
                data["container_stats"] = stats_after
                runs.append(data)
                
                print(f" Done! RPS: {data['rps']:.1f} | p50: {data['p50_us']/1000.0:.3f}ms | p99: {data['p99_us']/1000.0:.3f}ms")
                time.sleep(1)

            # Calculate aggregated metrics
            avg_rps = statistics.mean([r["rps"] for r in runs])
            stdev_rps = statistics.stdev([r["rps"] for r in runs]) if len(runs) > 1 else 0.0
            avg_p50_ms = statistics.mean([r["p50_us"] / 1000.0 for r in runs])
            avg_p75_ms = statistics.mean([r["p75_us"] / 1000.0 for r in runs])
            avg_p90_ms = statistics.mean([r["p90_us"] / 1000.0 for r in runs])
            avg_p99_ms = statistics.mean([r["p99_us"] / 1000.0 for r in runs])
            avg_max_ms = statistics.mean([r["max_latency_us"] / 1000.0 for r in runs])
            avg_lat_ms = statistics.mean([r["avg_latency_us"] / 1000.0 for r in runs])
            total_reqs = sum(r["total_requests"] for r in runs)
            total_errors = sum(r["errors"]["connect"] + r["errors"]["read"] + r["errors"]["write"] + r["errors"]["timeout"] for r in runs)
            total_msgs = sum(r["msg_delta"] for r in runs)

            all_results[tier_name][arm] = {
                "rps_mean": avg_rps,
                "rps_stdev": stdev_rps,
                "p50_ms": avg_p50_ms,
                "p75_ms": avg_p75_ms,
                "p90_ms": avg_p90_ms,
                "p99_ms": avg_p99_ms,
                "max_ms": avg_max_ms,
                "mean_lat_ms": avg_lat_ms,
                "total_requests": total_reqs,
                "total_errors": total_errors,
                "spoa_messages_recorded": total_msgs,
                "last_container_stats": runs[-1]["container_stats"]
            }

    # Save raw json output
    os.makedirs(".superpowers/benchmarks", exist_ok=True)
    json_path = ".superpowers/benchmarks/benchmark_results.json"
    with open(json_path, "w") as f:
        json.dump(all_results, f, indent=2)
    print(f"\n[+] Raw results saved to: {json_path}")

    # Print summary markdown table
    print("\n" + "=" * 80)
    print("BENCHMARK SUMMARY RESULTS")
    print("=" * 80)

    for tier_name, arms_data in all_results.items():
        print(f"\n### {tier_name}")
        base_rps = arms_data["Baseline (No SPOA)"]["rps_mean"]
        base_p50 = arms_data["Baseline (No SPOA)"]["p50_ms"]
        base_p99 = arms_data["Baseline (No SPOA)"]["p99_ms"]

        print(f"| Arm | Throughput (RPS) | Δ vs Base | p50 Latency | Δ p50 | p99 Latency | Δ p99 | Errors |")
        print(f"|---|---|---|---|---|---|---|---|")
        for arm_name, metrics in arms_data.items():
            rps = metrics["rps_mean"]
            rps_diff = ((rps - base_rps) / base_rps) * 100.0
            rps_diff_str = f"{rps_diff:+.2f}%" if arm_name != "Baseline (No SPOA)" else "BASELINE"
            
            p50 = metrics["p50_ms"]
            p50_diff = p50 - base_p50
            p50_diff_str = f"{p50_diff:+.3f} ms" if arm_name != "Baseline (No SPOA)" else "BASELINE"

            p99 = metrics["p99_ms"]
            p99_diff = p99 - base_p99
            p99_diff_str = f"{p99_diff:+.3f} ms" if arm_name != "Baseline (No SPOA)" else "BASELINE"
            
            errs = metrics["total_errors"]
            print(f"| {arm_name} | {rps:.1f} ±{metrics['rps_stdev']:.1f} | {rps_diff_str} | {p50:.3f} ms | {p50_diff_str} | {p99:.3f} ms | {p99_diff_str} | {errs} |")

if __name__ == "__main__":
    main()
