#!/usr/bin/env bash
# Hot-swap memory monitoring script for Maglev
# Monitors RSS, captures heap profiles, and triggers ForceUpdate
#
# Usage: ./scripts/hotswap-memory-test.sh
#
# Prerequisites:
#   - Server running with MAGLEV_ENABLE_PPROF=1
#   - k6 installed (optional, for load simulation)
#   - go tool pprof available

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
RESULTS_DIR="$REPO_ROOT/loadtest/results"
PPROF_DIR="$RESULTS_DIR/pprof"

# Configuration
BASE_URL="${MAGLEV_URL:-http://localhost:4000}"
SAMPLE_INTERVAL="${SAMPLE_INTERVAL:-1}"  # seconds
LOAD_TEST_DURATION="${LOAD_TEST_DURATION:-60}"  # seconds

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

log_info() {
    echo -e "${GREEN}[INFO]${NC} $1"
}

log_warn() {
    echo -e "${YELLOW}[WARN]${NC} $1"
}

log_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

# Check if server is running
check_server() {
    if ! curl -s "$BASE_URL/healthz" > /dev/null 2>&1; then
        log_error "Server not reachable at $BASE_URL"
        log_info "Start the server with: MAGLEV_ENABLE_PPROF=1 make run"
        exit 1
    fi
    log_info "Server is running at $BASE_URL"
}

# Check if pprof is enabled
check_pprof() {
    if ! curl -s "$BASE_URL/debug/pprof/" > /dev/null 2>&1; then
        log_warn "pprof not enabled. Start server with MAGLEV_ENABLE_PPROF=1"
        return 1
    fi
    log_info "pprof endpoint available"
    return 0
}

# Find maglev process
find_maglev_pid() {
    local pid
    pid=$(pgrep -f "bin/maglev" 2>/dev/null || pgrep -f "./maglev" 2>/dev/null || echo "")
    if [ -z "$pid" ]; then
        log_warn "Could not find maglev process - RSS monitoring disabled"
        echo ""
    else
        log_info "Found maglev process: PID $pid"
        echo "$pid"
    fi
}

# RSS monitoring function (background)
monitor_rss() {
    local pid=$1
    local output_file=$2
    local interval=$3
    
    echo "timestamp,rss_kb" > "$output_file"
    
    while true; do
        if [ -n "$pid" ] && [ -d "/proc/$pid" ]; then
            local rss
            rss=$(ps -o rss= -p "$pid" 2>/dev/null || echo "0")
            echo "$(date +%s),$rss" >> "$output_file"
        fi
        sleep "$interval"
    done
}

# Capture heap profile
capture_heap_profile() {
    local name=$1
    local output_file="$PPROF_DIR/heap_$name.pb.gz"
    
    log_info "Capturing heap profile: $name"
    curl -s "$BASE_URL/debug/pprof/heap" > "$output_file" 2>/dev/null || {
        log_warn "Failed to capture heap profile"
        return 1
    }
    log_info "Heap profile saved: $output_file"
}

# Trigger ForceUpdate via admin endpoint (if available) or wait
trigger_force_update() {
    log_warn "ForceUpdate must be triggered manually or by waiting for 24h cycle"
    log_info "For testing, you can modify the ticker interval in static.go"
    log_info "Or call manager.ForceUpdate() from a test"
}

# Main test flow
run_hotswap_test() {
    mkdir -p "$RESULTS_DIR" "$PPROF_DIR"
    
    local timestamp
    timestamp=$(date +%Y%m%d_%H%M%S)
    local rss_file="$RESULTS_DIR/rss_$timestamp.csv"
    local summary_file="$RESULTS_DIR/summary_$timestamp.txt"
    
    log_info "=== HOT-SWAP MEMORY TEST ==="
    log_info "Results directory: $RESULTS_DIR"
    
    check_server
    local pprof_available
    check_pprof && pprof_available=true || pprof_available=false
    
    local maglev_pid
    maglev_pid=$(find_maglev_pid)
    
    # Start RSS monitoring in background
    local rss_monitor_pid=""
    if [ -n "$maglev_pid" ]; then
        log_info "Starting RSS monitoring (every ${SAMPLE_INTERVAL}s)"
        monitor_rss "$maglev_pid" "$rss_file" "$SAMPLE_INTERVAL" &
        rss_monitor_pid=$!
    fi
    
    # Capture baseline heap profile
    if [ "$pprof_available" = true ]; then
        capture_heap_profile "baseline_$timestamp"
    fi
    
    # Record baseline RSS
    local baseline_rss=0
    if [ -n "$maglev_pid" ]; then
        baseline_rss=$(ps -o rss= -p "$maglev_pid" 2>/dev/null || echo "0")
        log_info "Baseline RSS: ${baseline_rss} KB ($(echo "scale=2; $baseline_rss/1024" | bc) MB)"
    fi
    
    # Optional: Start k6 load test in background
    if command -v k6 &> /dev/null; then
        log_info "Starting k6 load test for ${LOAD_TEST_DURATION}s"
       k6 run --duration "${LOAD_TEST_DURATION}s" --vus 50 \
            "$REPO_ROOT/loadtest/k6/hotswap_scenario.js" \
            > "$RESULTS_DIR/k6_output_$timestamp.txt" 2>&1 &
        local k6_pid=$!
        log_info "k6 running in background (PID: $k6_pid)"
    else
        log_warn "k6 not installed - skipping load simulation"
    fi
    
    log_info ""
    log_info "=== MONITORING IN PROGRESS ==="
    log_info "Press Ctrl+C to stop monitoring and generate summary"
    log_info ""
    log_info "To trigger ForceUpdate:"
    log_info "  Option 1: Wait for the 24h ticker (not practical)"
    log_info "  Option 2: Run the perftest: go test -tags=perftest -v -run TestHotSwapMemory_LargeAgency ./internal/gtfs/"
    log_info "  Option 3: Use the API test endpoint (if implemented)"
    log_info ""
    
    # Wait for interrupt
    trap 'cleanup' INT TERM
    
    # Monitor RSS and show updates
    while true; do
        if [ -n "$maglev_pid" ] && [ -d "/proc/$maglev_pid" ]; then
            local current_rss
            current_rss=$(ps -o rss= -p "$maglev_pid" 2>/dev/null || echo "0")
            local multiplier
            if [ "$baseline_rss" -gt 0 ]; then
                multiplier=$(echo "scale=2; $current_rss / $baseline_rss" | bc)
            else
                multiplier="N/A"
            fi
            printf "\r[%s] RSS: %s KB (%.2f MB) | Multiplier: %sx    " \
                "$(date +%H:%M:%S)" \
                "$current_rss" \
                "$(echo "scale=2; $current_rss/1024" | bc)" \
                "$multiplier"
        fi
        sleep 2
    done
}

cleanup() {
    echo ""
    log_info "Stopping monitoring..."
    
    # Kill background processes
    jobs -p | xargs -r kill 2>/dev/null || true
    
    # Capture final heap profile
    if check_pprof 2>/dev/null; then
        capture_heap_profile "final_$(date +%Y%m%d_%H%M%S)"
    fi
    
    # Generate summary
    generate_summary
    
    exit 0
}

generate_summary() {
    log_info "=== GENERATING SUMMARY ==="
    
    local summary_file="$RESULTS_DIR/summary_$(date +%Y%m%d_%H%M%S).txt"
    
    {
        echo "Hot-Swap Memory Test Summary"
        echo "============================"
        echo "Date: $(date)"
        echo ""
        
        # Find latest RSS file
        local latest_rss
        latest_rss=$(ls -t "$RESULTS_DIR"/rss_*.csv 2>/dev/null | head -1)
        
        if [ -n "$latest_rss" ] && [ -f "$latest_rss" ]; then
            echo "RSS Data Analysis:"
            echo "------------------"
            
            # Calculate min, max, avg
            local min max avg count
            min=$(tail -n +2 "$latest_rss" | cut -d, -f2 | sort -n | head -1)
            max=$(tail -n +2 "$latest_rss" | cut -d, -f2 | sort -n | tail -1)
            count=$(tail -n +2 "$latest_rss" | wc -l)
            avg=$(tail -n +2 "$latest_rss" | cut -d, -f2 | awk '{s+=$1} END {printf "%.0f", s/NR}')
            
            echo "  Samples: $count"
            echo "  Min RSS: $min KB ($(echo "scale=2; $min/1024" | bc) MB)"
            echo "  Max RSS: $max KB ($(echo "scale=2; $max/1024" | bc) MB)"
            echo "  Avg RSS: $avg KB ($(echo "scale=2; $avg/1024" | bc) MB)"
            
            if [ "$min" -gt 0 ]; then
                local peak_multiplier
                peak_multiplier=$(echo "scale=2; $max / $min" | bc)
                echo "  Peak Multiplier: ${peak_multiplier}x"
            fi
            echo ""
        fi
        
        echo "Heap Profiles (if captured):"
        echo "----------------------------"
        ls -la "$PPROF_DIR"/*.pb.gz 2>/dev/null || echo "  No profiles found"
        echo ""
        
        echo "To analyze heap profiles:"
        echo "  go tool pprof $PPROF_DIR/heap_baseline_*.pb.gz"
        echo "  go tool pprof -diff_base=$PPROF_DIR/heap_baseline_*.pb.gz $PPROF_DIR/heap_final_*.pb.gz"
        echo ""
        
        echo "Recommendations:"
        echo "----------------"
        if [ -n "$max" ] && [ "$max" -gt 0 ]; then
            local max_mb
            max_mb=$(echo "scale=0; $max/1024" | bc)
            local recommended
            recommended=$(echo "scale=0; $max_mb * 2.5" | bc)
            echo "  Based on peak RSS of ${max_mb} MB:"
            echo "  Recommended container memory: ${recommended} MB (2.5x peak for safety)"
        fi
        
    } | tee "$summary_file"
    
    log_info "Summary saved: $summary_file"
}

# Parse arguments
case "${1:-}" in
    "monitor")
        run_hotswap_test
        ;;
    "profile")
        mkdir -p "$PPROF_DIR"
        check_server
        capture_heap_profile "manual_$(date +%Y%m%d_%H%M%S)"
        ;;
    "summary")
        generate_summary
        ;;
    *)
        echo "Maglev Hot-Swap Memory Test Script"
        echo ""
        echo "Usage: $0 <command>"
        echo ""
        echo "Commands:"
        echo "  monitor  - Start continuous RSS monitoring and profile capture"
        echo "  profile  - Capture a single heap profile"
        echo "  summary  - Generate summary from existing data"
        echo ""
        echo "Environment variables:"
        echo "  MAGLEV_URL         - Server URL (default: http://localhost:4000)"
        echo "  SAMPLE_INTERVAL    - RSS sample interval in seconds (default: 1)"
        echo "  LOAD_TEST_DURATION - k6 load test duration (default: 60)"
        echo ""
        echo "Example workflow:"
        echo "  1. Start server: MAGLEV_ENABLE_PPROF=1 make run"
        echo "  2. Start monitoring: ./scripts/hotswap-memory-test.sh monitor"
        echo "  3. In another terminal, trigger ForceUpdate or run perftest"
        echo "  4. Press Ctrl+C to stop and see summary"
        ;;
esac
