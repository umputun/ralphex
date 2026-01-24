// ralphex dashboard - SSE streaming and UI handling
//
// XSS Prevention Strategy:
// - All user/server-provided text is rendered via textContent or createTextNode
// - escapeHtml() is used for any text embedded in HTML strings (export feature)
// - innerHTML is only used with static HTML or previously-sanitized DOM clones
// - Search highlighting uses DOM manipulation, not string interpolation
//
(function() {
    'use strict';

    // DOM elements
    const output = document.getElementById('output');
    const statusBadge = document.getElementById('status-badge');
    const elapsedTimeEl = document.getElementById('elapsed-time');
    const searchInput = document.getElementById('search');
    const scrollIndicator = document.getElementById('scroll-indicator');
    const scrollToBottomBtn = document.getElementById('scroll-to-bottom');
    const phaseTabs = document.querySelectorAll('.phase-tab');
    const mainContainer = document.querySelector('.main-container');
    const outputPanel = document.querySelector('.output-panel');
    const planToggle = document.getElementById('plan-toggle');
    const planContent = document.getElementById('plan-content');
    const exportBtn = document.getElementById('export-btn');
    const expandAllBtn = document.getElementById('expand-all');
    const collapseAllBtn = document.getElementById('collapse-all');

    // session sidebar elements
    const sessionSidebar = document.getElementById('session-sidebar');
    const sessionList = document.getElementById('session-list');
    const sidebarToggle = document.getElementById('sidebar-toggle');
    const mainWrapper = document.getElementById('main-wrapper');
    const planNameEl = document.getElementById('plan-name');
    const branchNameEl = document.getElementById('branch-name');

    // SSE reconnection constants
    var SSE_INITIAL_RECONNECT_MS = 1000;
    var SSE_MAX_RECONNECT_MS = 30000;

    // session polling interval
    var SESSION_POLL_INTERVAL_MS = 5000;

    // application state - encapsulated for easier testing and debugging
    var state = {
        // UI state
        autoScroll: true,
        currentPhase: 'all',
        currentSection: null,
        searchTerm: '',
        searchTimeout: null,
        planCollapsed: localStorage.getItem('planCollapsed') === 'true',
        sidebarCollapsed: localStorage.getItem('sidebarCollapsed') === 'true',
        planData: null,

        // session state
        sessions: [],
        currentSessionId: null,
        sessionPollInterval: null,

        // timing state
        executionStartTime: null,
        lastEventTimestamp: null, // tracks most recent event timestamp for duration calculations
        sectionStartTimes: {},
        elapsedTimerInterval: null,
        sectionCounter: 0, // monotonically increasing counter for unique section IDs
        isTerminalState: false, // true when COMPLETED/FAILED signal received
        seenSections: {}, // track seen sections to avoid duplicates

        // SSE connection state
        reconnectDelay: SSE_INITIAL_RECONNECT_MS,
        currentEventSource: null,
        isFirstConnect: true,
        resetOnNextEvent: false,

        // event batching state for performance
        eventQueue: [],
        isProcessingQueue: false,
        pendingScrollRestore: false
    };

    // initialize plan panel state
    if (state.planCollapsed) {
        mainContainer.classList.add('plan-collapsed');
        planToggle.textContent = '▶';
    }

    // initialize sidebar state
    if (state.sidebarCollapsed) {
        document.body.classList.add('sidebar-collapsed');
        sidebarToggle.textContent = '▶';
    }

    // batch size for event queue processing
    var BATCH_SIZE = 100;

    // process event queue in batches using requestAnimationFrame
    // this prevents layout thrashing when loading sessions with many events
    function processEventQueue() {
        if (state.isProcessingQueue || state.eventQueue.length === 0) return;
        state.isProcessingQueue = true;

        var shouldRestoreScroll = state.pendingScrollRestore;
        state.pendingScrollRestore = false;
        var savedAutoScroll = state.autoScroll;
        state.autoScroll = false; // disable per-event scrolling during batch

        requestAnimationFrame(function processBatch() {
            var batch = state.eventQueue.splice(0, BATCH_SIZE);
            for (var i = 0; i < batch.length; i++) {
                renderEvent(batch[i]);
            }
            if (state.eventQueue.length > 0) {
                requestAnimationFrame(processBatch);
            } else {
                state.autoScroll = savedAutoScroll;
                if (shouldRestoreScroll) {
                    if (!restoreScrollPosition(state.currentSessionId)) {
                        outputPanel.scrollTop = outputPanel.scrollHeight;
                    }
                } else if (state.autoScroll) {
                    outputPanel.scrollTop = outputPanel.scrollHeight;
                }
                state.isProcessingQueue = false;
            }
        });
    }

    // initialize current session from URL hash or localStorage
    function initCurrentSession() {
        var hash = window.location.hash.slice(1);
        if (hash) {
            state.currentSessionId = hash;
        } else {
            var saved = localStorage.getItem('currentSessionId');
            if (saved) {
                state.currentSessionId = saved;
            }
        }
    }
    initCurrentSession();

    // format timestamp for display (time only)
    function formatTimestamp(ts) {
        const d = new Date(ts);
        const pad = function(n) { return n.toString().padStart(2, '0'); };
        return pad(d.getHours()) + ':' +
               pad(d.getMinutes()) + ':' +
               pad(d.getSeconds());
    }

    // format duration for display
    function formatDuration(ms) {
        if (ms < 0) ms = 0;
        const seconds = Math.floor(ms / 1000);
        const minutes = Math.floor(seconds / 60);
        const hours = Math.floor(minutes / 60);

        if (hours > 0) {
            return hours + 'h ' + (minutes % 60) + 'm';
        } else if (minutes > 0) {
            return minutes + 'm ' + (seconds % 60) + 's';
        } else {
            return seconds + 's';
        }
    }

    // escape regex special characters for safe regex creation
    function escapeRegex(str) {
        return str.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
    }

    /**
     * Escape HTML special characters to prevent XSS attacks.
     * Uses the browser's built-in text node encoding for safety.
     * @param {string} str - The untrusted string to escape
     * @returns {string} HTML-safe string with special chars encoded
     */
    function escapeHtml(str) {
        if (!str) return '';
        var div = document.createElement('div');
        div.textContent = str;
        return div.innerHTML;
    }

    /**
     * Set text content with optional search highlighting.
     * XSS-safe: uses textContent and createTextNode for untrusted text,
     * only injects highlight spans which are safe DOM elements.
     * @param {Element} element - The DOM element to update
     * @param {string} text - The text content (may be untrusted)
     * @param {string} term - The search term to highlight
     */
    function setContentWithHighlight(element, text, term) {
        element.textContent = '';

        if (!term) {
            element.textContent = text;
            return;
        }

        try {
            const regex = new RegExp('(' + escapeRegex(term) + ')', 'gi');
            const parts = text.split(regex);

            parts.forEach(function(part) {
                if (part.toLowerCase() === term.toLowerCase()) {
                    const highlight = document.createElement('span');
                    highlight.className = 'highlight';
                    highlight.textContent = part;
                    element.appendChild(highlight);
                } else if (part) {
                    element.appendChild(document.createTextNode(part));
                }
            });
        } catch (e) {
            element.textContent = text;
        }
    }

    // check if text matches search term
    function matchesSearch(text, term) {
        if (!term) return true;
        return text.toLowerCase().includes(term.toLowerCase());
    }

    // create output line element
    function createOutputLine(event) {
        const line = document.createElement('div');
        line.className = 'output-line';
        line.dataset.phase = event.phase;
        line.dataset.type = event.type;

        const timestamp = document.createElement('span');
        timestamp.className = 'timestamp';
        timestamp.textContent = formatTimestamp(event.timestamp);

        const content = document.createElement('span');
        content.className = 'content';
        content.dataset.originalText = event.text;
        setContentWithHighlight(content, event.text, state.searchTerm);

        line.appendChild(timestamp);
        line.appendChild(content);

        // apply phase filter
        if (state.currentPhase !== 'all' && event.phase !== state.currentPhase) {
            line.classList.add('hidden');
        }

        // apply search filter
        if (state.searchTerm && !matchesSearch(event.text, state.searchTerm)) {
            line.classList.add('hidden');
        }

        return line;
    }

    // create section header (collapsible details element)
    // uses monotonically increasing counter for unique section IDs to avoid collisions on duplicate titles
    function createSectionHeader(event) {
        state.sectionCounter++;
        var sectionId = 'section-' + state.sectionCounter;

        const details = document.createElement('details');
        details.className = 'section-header';
        details.dataset.phase = event.phase;
        details.dataset.sectionId = sectionId;
        details.open = true;

        const summary = document.createElement('summary');

        const phaseLabel = document.createElement('span');
        phaseLabel.className = 'section-phase';
        phaseLabel.textContent = event.phase;

        const title = document.createElement('span');
        title.className = 'section-title';
        title.textContent = event.section || event.text;

        const duration = document.createElement('span');
        duration.className = 'section-duration';
        duration.textContent = '';

        summary.appendChild(phaseLabel);
        summary.appendChild(title);
        summary.appendChild(duration);

        const content = document.createElement('div');
        content.className = 'section-content';

        details.appendChild(summary);
        details.appendChild(content);

        // apply phase filter
        if (state.currentPhase !== 'all' && event.phase !== state.currentPhase) {
            details.classList.add('hidden');
        }

        // track section start time for duration
        state.sectionStartTimes[sectionId] = new Date(event.timestamp).getTime();

        return details;
    }

    // update section duration display - uses direct selector for O(1) lookup
    // endTimestamp is optional - if provided, uses it instead of calculating from session state
    function updateSectionDuration(sectionId, endTimestamp) {
        var startTime = state.sectionStartTimes[sectionId];
        if (!startTime) return;

        // use provided endTimestamp, or Date.now() for live sessions, or lastEventTimestamp for historical
        var endTime = endTimestamp || (isLiveSession() ? Date.now() : (state.lastEventTimestamp || Date.now()));
        const duration = endTime - startTime;
        if (duration < 0) return; // guard against negative durations

        const section = output.querySelector('.section-header[data-section-id="' + CSS.escape(sectionId) + '"]');
        if (section) {
            const durationEl = section.querySelector('.section-duration');
            if (durationEl) {
                durationEl.textContent = formatDuration(duration);
            }
        }
    }

    // finalize section duration when a new section starts
    // endTimestamp is the new section's start time (used for accurate duration)
    // also cleans up sectionStartTimes to prevent memory growth
    function finalizePreviousSectionDuration(endTimestamp) {
        if (state.currentSection) {
            var sectionId = state.currentSection.dataset.sectionId;
            updateSectionDuration(sectionId, endTimestamp);
            // clean up the start time entry to prevent memory growth
            delete state.sectionStartTimes[sectionId];
        }
    }

    // update status badge based on event
    function updateStatusBadge(event) {
        // don't update badge after terminal state reached
        if (state.isTerminalState) {
            return;
        }

        statusBadge.className = 'status-badge';

        if (event.type === 'signal') {
            if (event.signal === 'COMPLETED' || event.signal === 'REVIEW_DONE' || event.signal === 'CODEX_REVIEW_DONE') {
                statusBadge.textContent = 'COMPLETED';
                statusBadge.classList.add('completed');
                state.isTerminalState = true;
                // update timers one final time before stopping
                updateTimers();
                if (state.elapsedTimerInterval) {
                    clearInterval(state.elapsedTimerInterval);
                    state.elapsedTimerInterval = null;
                }
            } else if (event.signal === 'FAILED') {
                statusBadge.textContent = 'FAILED';
                statusBadge.classList.add('failed');
                state.isTerminalState = true;
                // update timers one final time before stopping
                updateTimers();
                if (state.elapsedTimerInterval) {
                    clearInterval(state.elapsedTimerInterval);
                    state.elapsedTimerInterval = null;
                }
            }
            return;
        }

        // update based on phase
        switch (event.phase) {
            case 'task':
                statusBadge.textContent = 'TASK';
                statusBadge.classList.add('task', 'pulse');
                break;
            case 'review':
                statusBadge.textContent = 'REVIEW';
                statusBadge.classList.add('review', 'pulse');
                break;
            case 'codex':
                statusBadge.textContent = 'CODEX';
                statusBadge.classList.add('codex', 'pulse');
                break;
            case 'claude-eval':
                statusBadge.textContent = 'EVAL';
                statusBadge.classList.add('review', 'pulse');
                break;
        }
    }

    // check if current session is live (active and events are recent)
    function isLiveSession() {
        if (state.isTerminalState) return false;
        // check if we have recent events (within last 60 seconds)
        if (state.lastEventTimestamp) {
            var age = Date.now() - state.lastEventTimestamp;
            return age < 60000; // consider live if last event was within 60s
        }
        return false;
    }

    // update elapsed time display and current section duration
    function updateTimers() {
        if (!state.executionStartTime) return;
        // for live sessions use Date.now(), for historical use lastEventTimestamp
        var endTime = isLiveSession() ? Date.now() : (state.lastEventTimestamp || Date.now());
        var elapsed = endTime - state.executionStartTime;
        elapsedTimeEl.textContent = formatDuration(elapsed);

        // update current section duration
        if (state.currentSection) {
            var sectionId = state.currentSection.dataset.sectionId;
            updateSectionDuration(sectionId);
        }
    }

    // start elapsed time timer - clears any existing interval to prevent memory leaks on reconnect
    function startElapsedTimer() {
        if (state.elapsedTimerInterval) {
            clearInterval(state.elapsedTimerInterval);
        }
        state.elapsedTimerInterval = setInterval(updateTimers, 1000);
    }

    // handle task start event
    function handleTaskStart(event) {
        updatePlanTaskStatus(event.task_num, 'active');
    }

    // handle task end event
    function handleTaskEnd(event) {
        updatePlanTaskStatus(event.task_num, 'done');
    }

    // update plan task status - uses direct selector for O(1) lookup
    function updatePlanTaskStatus(taskNum, statusValue) {
        if (!state.planData) return;

        const taskEl = planContent.querySelector('.plan-task[data-task-num="' + taskNum + '"]');
        if (!taskEl) return;

        taskEl.classList.remove('active');
        const statusEl = taskEl.querySelector('.plan-task-status');
        statusEl.classList.remove('pending', 'active', 'done', 'failed');
        statusEl.classList.add(statusValue);

        if (statusValue === 'active') {
            taskEl.classList.add('active');
            statusEl.textContent = '●';
        } else if (statusValue === 'done') {
            statusEl.textContent = '✓';
            // mark all checkboxes as checked when task is done
            const checkboxes = taskEl.querySelectorAll('.plan-checkbox');
            checkboxes.forEach(function(cb) {
                cb.classList.add('checked');
                const icon = cb.querySelector('.plan-checkbox-icon');
                if (icon) {
                    icon.classList.add('checked');
                    icon.textContent = '☑';
                }
            });
        } else if (statusValue === 'failed') {
            statusEl.textContent = '✗';
        } else {
            statusEl.textContent = '○';
        }
    }

    // render event to output
    function renderEvent(event) {
        var eventTimestamp = new Date(event.timestamp).getTime();

        // track execution start time
        if (!state.executionStartTime) {
            state.executionStartTime = eventTimestamp;
            startElapsedTimer();
        }

        // always update lastEventTimestamp for duration calculations
        state.lastEventTimestamp = eventTimestamp;

        // update status badge
        updateStatusBadge(event);

        // handle task boundary events
        if (event.type === 'task_start') {
            handleTaskStart(event);
            return; // don't render as output
        }
        if (event.type === 'task_end') {
            handleTaskEnd(event);
            return; // don't render as output
        }
        if (event.type === 'iteration_start') {
            // iteration events are informational
            return;
        }

        if (event.type === 'section') {
            // deduplicate sections (can happen when BroadcastLogger and Tailer both emit)
            var sectionKey = event.section + '|' + event.timestamp;
            if (state.seenSections[sectionKey]) {
                return; // skip duplicate section
            }
            state.seenSections[sectionKey] = true;

            // finalize previous section duration using this section's timestamp as end time
            finalizePreviousSectionDuration(eventTimestamp);
            // create new collapsible section
            state.currentSection = createSectionHeader(event);
            output.appendChild(state.currentSection);
        } else if (event.type === 'signal' && (event.signal === 'COMPLETED' || event.signal === 'FAILED')) {
            // render completion message for terminal signals
            var completionText = event.signal === 'COMPLETED' ? 'execution completed successfully' : 'execution failed';
            var completionEvent = {
                timestamp: event.timestamp,
                phase: event.phase,
                text: completionText,
                type: 'output'
            };
            var line = createOutputLine(completionEvent);
            if (state.currentSection) {
                var content = state.currentSection.querySelector('.section-content');
                content.appendChild(line);
            } else {
                output.appendChild(line);
            }
        } else {
            // create output line
            var line = createOutputLine(event);

            // add to current section or root output
            if (state.currentSection) {
                var content = state.currentSection.querySelector('.section-content');
                content.appendChild(line);
            } else {
                output.appendChild(line);
            }
        }

        // auto-scroll if enabled
        if (state.autoScroll) {
            outputPanel.scrollTop = outputPanel.scrollHeight;
        }
    }

    // connect to SSE stream with exponential backoff
    function connect() {
        if (!state.isFirstConnect) {
            state.resetOnNextEvent = true;
        }

        var url = '/events';
        if (state.currentSessionId) {
            url += '?session=' + encodeURIComponent(state.currentSessionId);
        }

        var source = new EventSource(url);
        state.currentEventSource = source;

        source.onopen = function() {
            // reset backoff and first-connect flag on successful connection
            state.reconnectDelay = SSE_INITIAL_RECONNECT_MS;
            state.isFirstConnect = false;
        };

        source.onmessage = function(e) {
            try {
                var event = JSON.parse(e.data);
                if (state.resetOnNextEvent) {
                    resetOutputState();
                    state.resetOnNextEvent = false;
                }
                // queue event for batch processing to avoid layout thrashing
                state.eventQueue.push(event);
                processEventQueue();
            } catch (err) {
                console.error('parse error:', err);
            }
        };

        source.onerror = function() {
            source.close();
            state.currentEventSource = null;

            // exponential backoff with max delay
            setTimeout(connect, state.reconnectDelay);
            state.reconnectDelay = Math.min(state.reconnectDelay * 2, SSE_MAX_RECONNECT_MS);
        };
    }

    // phase filter functions
    function setPhaseFilter(phase) {
        state.currentPhase = phase;

        phaseTabs.forEach(function(tab) {
            if (tab.dataset.phase === phase) {
                tab.classList.add('active');
            } else {
                tab.classList.remove('active');
            }
        });

        applyFilters();
    }

    // apply all current filters (phase + search)
    function applyFilters() {
        var sections = output.querySelectorAll('.section-header');
        sections.forEach(function(section) {
            var phase = section.dataset.phase;
            var phaseMatch = state.currentPhase === 'all' || phase === state.currentPhase;

            var hasSearchMatch = !state.searchTerm;
            if (state.searchTerm) {
                var lines = section.querySelectorAll('.output-line');
                lines.forEach(function(line) {
                    var contentEl = line.querySelector('.content');
                    var originalText = contentEl.dataset.originalText || contentEl.textContent;
                    if (matchesSearch(originalText, state.searchTerm)) {
                        hasSearchMatch = true;
                    }
                });
            }

            if (phaseMatch && hasSearchMatch) {
                section.classList.remove('hidden');
            } else {
                section.classList.add('hidden');
            }
        });

        var allLines = output.querySelectorAll('.output-line');
        allLines.forEach(function(line) {
            var phase = line.dataset.phase;
            var contentEl = line.querySelector('.content');
            var originalText = contentEl.dataset.originalText || contentEl.textContent;

            var phaseMatch = state.currentPhase === 'all' || phase === state.currentPhase;
            var searchMatch = !state.searchTerm || matchesSearch(originalText, state.searchTerm);

            if (phaseMatch && searchMatch) {
                line.classList.remove('hidden');
            } else {
                line.classList.add('hidden');
            }

            setContentWithHighlight(contentEl, originalText, state.searchTerm);
        });
    }

    // handle search input with debounce
    function handleSearch() {
        state.searchTerm = searchInput.value.trim();
        applyFilters();
    }

    // debounced search
    function debouncedSearch() {
        clearTimeout(state.searchTimeout);
        state.searchTimeout = setTimeout(handleSearch, 150);
    }

    // scroll tracking
    function checkScroll() {
        var atBottom = outputPanel.scrollHeight - outputPanel.scrollTop - outputPanel.clientHeight < 50;

        if (atBottom) {
            state.autoScroll = true;
            scrollIndicator.classList.remove('visible');
        } else {
            scrollIndicator.classList.add('visible');
        }
    }

    // manual scroll disables auto-scroll
    function handleManualScroll() {
        var atBottom = outputPanel.scrollHeight - outputPanel.scrollTop - outputPanel.clientHeight < 50;
        if (!atBottom) {
            state.autoScroll = false;
        }
    }

    // scroll to bottom and re-enable auto-scroll
    function scrollToBottom() {
        outputPanel.scrollTop = outputPanel.scrollHeight;
        state.autoScroll = true;
        scrollIndicator.classList.remove('visible');
    }

    // toggle plan panel
    function togglePlanPanel() {
        state.planCollapsed = !state.planCollapsed;
        localStorage.setItem('planCollapsed', state.planCollapsed);

        if (state.planCollapsed) {
            mainContainer.classList.add('plan-collapsed');
            planToggle.textContent = '▶';
        } else {
            mainContainer.classList.remove('plan-collapsed');
            planToggle.textContent = '◀';
        }
    }

    // toggle session sidebar
    function toggleSessionSidebar() {
        state.sidebarCollapsed = !state.sidebarCollapsed;
        localStorage.setItem('sidebarCollapsed', state.sidebarCollapsed);

        if (state.sidebarCollapsed) {
            document.body.classList.add('sidebar-collapsed');
            sidebarToggle.textContent = '▶';
        } else {
            document.body.classList.remove('sidebar-collapsed');
            sidebarToggle.textContent = '◀';
        }
    }

    // fetch sessions from API
    function fetchSessions() {
        fetch('/api/sessions')
            .then(function(response) {
                if (!response.ok) {
                    throw new Error('Sessions not available');
                }
                return response.json();
            })
            .then(function(sessions) {
                state.sessions = sessions;
                renderSessionList(sessions);
                // auto-select first session if none is currently selected
                if (!state.currentSessionId && sessions.length > 0) {
                    selectSession(sessions[0].id);
                }
            })
            .catch(function(err) {
                clearElement(sessionList);
                var msg = document.createElement('div');
                msg.className = 'session-loading';
                msg.textContent = 'No sessions found';
                sessionList.appendChild(msg);
                console.log('Sessions fetch:', err.message);
            });
    }

    // format relative time for display
    function formatRelativeTime(date) {
        var now = Date.now();
        var diff = now - new Date(date).getTime();

        if (diff < 0) return 'just now';

        var seconds = Math.floor(diff / 1000);
        var minutes = Math.floor(seconds / 60);
        var hours = Math.floor(minutes / 60);
        var days = Math.floor(hours / 24);

        if (days > 0) {
            return days + 'd ago';
        } else if (hours > 0) {
            return hours + 'h ago';
        } else if (minutes > 0) {
            return minutes + 'm ago';
        } else {
            return 'just now';
        }
    }

    // extract plan name from path
    function extractPlanName(path) {
        if (!path) return 'Unknown';
        var parts = path.split('/');
        var filename = parts[parts.length - 1];
        return filename.replace(/\.md$/i, '');
    }

    // save scroll position for a session to localStorage
    function saveScrollPosition(sessionId) {
        if (sessionId) {
            localStorage.setItem('scroll_' + sessionId, outputPanel.scrollTop);
        }
    }

    // restore scroll position for a session from localStorage
    // returns true if position was restored, false otherwise
    function restoreScrollPosition(sessionId) {
        var saved = localStorage.getItem('scroll_' + sessionId);
        if (saved !== null) {
            outputPanel.scrollTop = parseInt(saved, 10);
            return true;
        }
        return false;
    }

    /**
     * Render session list to sidebar.
     * XSS-safe: uses textContent for all user-provided text.
     * @param {Array} sessions - Array of session objects from API
     */
    function renderSessionList(sessions) {
        clearElement(sessionList);

        if (!sessions || sessions.length === 0) {
            var msg = document.createElement('div');
            msg.className = 'session-loading';
            msg.textContent = 'No sessions found';
            sessionList.appendChild(msg);
            return;
        }

        // sessions are already sorted by recency from API
        sessions.forEach(function(session) {
            var item = document.createElement('div');
            item.className = 'session-item';
            item.dataset.sessionId = session.id;

            if (session.id === state.currentSessionId) {
                item.classList.add('selected');
            }

            // status indicator
            var indicator = document.createElement('span');
            indicator.className = 'session-indicator';
            if (session.state === 'active') {
                indicator.classList.add('active');
                indicator.title = 'Active session';
            } else {
                indicator.classList.add('completed');
                indicator.title = 'Completed session';
            }

            // session info container
            var info = document.createElement('div');
            info.className = 'session-info';

            // plan name
            var name = document.createElement('div');
            name.className = 'session-name';
            name.textContent = extractPlanName(session.planPath);

            // branch and time
            var meta = document.createElement('div');
            meta.className = 'session-meta';

            if (session.branch) {
                var branchSpan = document.createElement('span');
                branchSpan.className = 'session-branch';
                branchSpan.textContent = session.branch;
                meta.appendChild(branchSpan);
            }

            var timeSpan = document.createElement('span');
            timeSpan.className = 'session-time';
            timeSpan.textContent = formatRelativeTime(session.lastModified);
            meta.appendChild(timeSpan);

            info.appendChild(name);
            info.appendChild(meta);

            item.appendChild(indicator);
            item.appendChild(info);

            // click handler
            item.addEventListener('click', function() {
                selectSession(session.id);
            });

            sessionList.appendChild(item);
        });
    }

    // select a session and switch to it
    function selectSession(sessionId) {
        if (sessionId === state.currentSessionId) {
            return; // already selected
        }

        // save scroll position of current session before switching
        saveScrollPosition(state.currentSessionId);

        state.currentSessionId = sessionId;

        // persist selection
        localStorage.setItem('currentSessionId', sessionId);
        window.location.hash = sessionId;

        // update UI selection
        var items = sessionList.querySelectorAll('.session-item');
        items.forEach(function(item) {
            if (item.dataset.sessionId === sessionId) {
                item.classList.add('selected');
            } else {
                item.classList.remove('selected');
            }
        });

        // find session data
        var session = null;
        for (var i = 0; i < state.sessions.length; i++) {
            if (state.sessions[i].id === sessionId) {
                session = state.sessions[i];
                break;
            }
        }

        // update header info
        if (session) {
            if (planNameEl) {
                planNameEl.textContent = extractPlanName(session.planPath);
            }
            if (branchNameEl) {
                branchNameEl.textContent = session.branch || '';
            }
        }

        // reconnect SSE to new session
        reconnectToSession(sessionId);

        // reload plan for new session
        fetchPlanForSession(sessionId);
    }

    // reconnect SSE stream to a specific session
    function reconnectToSession(sessionId) {
        // close existing connection
        if (state.currentEventSource) {
            state.currentEventSource.close();
            state.currentEventSource = null;
        }

        // reset output state
        resetOutputState();
        state.isFirstConnect = true;
        state.reconnectDelay = SSE_INITIAL_RECONNECT_MS;
        state.pendingScrollRestore = true; // restore scroll position after events load

        // connect to new session
        connect();
    }

    // fetch plan for a specific session
    function fetchPlanForSession(sessionId) {
        var url = '/api/plan';
        if (sessionId) {
            url += '?session=' + encodeURIComponent(sessionId);
        }

        fetch(url)
            .then(function(response) {
                if (!response.ok) {
                    throw new Error('Plan not available');
                }
                return response.json();
            })
            .then(function(plan) {
                state.planData = plan;
                renderPlan(plan);
            })
            .catch(function(err) {
                clearElement(planContent);
                planContent.appendChild(createPlanMessage('Plan not available'));
                console.log('Plan fetch:', err.message);
            });
    }

    // start polling for session updates
    function startSessionPolling() {
        if (state.sessionPollInterval) {
            clearInterval(state.sessionPollInterval);
        }
        state.sessionPollInterval = setInterval(fetchSessions, SESSION_POLL_INTERVAL_MS);
    }

    // stop polling for session updates
    function stopSessionPolling() {
        if (state.sessionPollInterval) {
            clearInterval(state.sessionPollInterval);
            state.sessionPollInterval = null;
        }
    }

    // clear element children using DOM methods
    function clearElement(el) {
        while (el.firstChild) {
            el.removeChild(el.firstChild);
        }
    }

    function resetOutputState() {
        clearElement(output);
        state.currentSection = null;
        state.sectionStartTimes = {};
        state.sectionCounter = 0;
        state.executionStartTime = null;
        state.lastEventTimestamp = null;
        state.isTerminalState = false;
        state.seenSections = {};
        state.eventQueue = [];
        state.isProcessingQueue = false;
        if (state.elapsedTimerInterval) {
            clearInterval(state.elapsedTimerInterval);
            state.elapsedTimerInterval = null;
        }
        elapsedTimeEl.textContent = '';
    }

    // create plan loading/error message element
    function createPlanMessage(text) {
        const div = document.createElement('div');
        div.className = 'plan-loading';
        div.textContent = text;
        return div;
    }

    // fetch and render plan
    function fetchPlan() {
        fetch('/api/plan')
            .then(function(response) {
                if (!response.ok) {
                    throw new Error('Plan not available');
                }
                return response.json();
            })
            .then(function(plan) {
                state.planData = plan;
                renderPlan(plan);
            })
            .catch(function(err) {
                clearElement(planContent);
                planContent.appendChild(createPlanMessage('Plan not available'));
                console.log('Plan fetch:', err.message);
            });
    }

    /**
     * Render plan to plan panel using DOM methods.
     * XSS-safe: uses textContent for all user-provided text,
     * never uses innerHTML with untrusted content.
     * @param {Object} plan - The plan data from the API
     */
    function renderPlan(plan) {
        clearElement(planContent);

        if (!plan.tasks || plan.tasks.length === 0) {
            planContent.appendChild(createPlanMessage('No tasks in plan'));
            return;
        }

        plan.tasks.forEach(function(task) {
            const taskEl = document.createElement('div');
            taskEl.className = 'plan-task';
            taskEl.dataset.taskNum = task.number;

            if (task.status === 'active') {
                taskEl.classList.add('active');
            }

            const header = document.createElement('div');
            header.className = 'plan-task-header';

            const statusIcon = document.createElement('span');
            statusIcon.className = 'plan-task-status ' + task.status;
            switch (task.status) {
                case 'pending': statusIcon.textContent = '○'; break;
                case 'active': statusIcon.textContent = '●'; break;
                case 'done': statusIcon.textContent = '✓'; break;
                case 'failed': statusIcon.textContent = '✗'; break;
                default: statusIcon.textContent = '○';
            }

            const title = document.createElement('span');
            title.className = 'plan-task-title';
            title.textContent = 'Task ' + task.number + ': ' + task.title;

            header.appendChild(statusIcon);
            header.appendChild(title);
            taskEl.appendChild(header);

            // render checkboxes
            task.checkboxes.forEach(function(checkbox) {
                const cbEl = document.createElement('div');
                cbEl.className = 'plan-checkbox';
                if (checkbox.checked) {
                    cbEl.classList.add('checked');
                }

                const icon = document.createElement('span');
                icon.className = 'plan-checkbox-icon';
                if (checkbox.checked) {
                    icon.classList.add('checked');
                    icon.textContent = '☑';
                } else {
                    icon.textContent = '☐';
                }

                const text = document.createElement('span');
                text.className = 'plan-checkbox-text';
                text.textContent = checkbox.text;

                cbEl.appendChild(icon);
                cbEl.appendChild(text);
                taskEl.appendChild(cbEl);
            });

            planContent.appendChild(taskEl);
        });
    }

    // event listeners
    phaseTabs.forEach(function(tab) {
        tab.addEventListener('click', function() {
            setPhaseFilter(tab.dataset.phase);
        });
    });

    searchInput.addEventListener('input', debouncedSearch);

    planToggle.addEventListener('click', togglePlanPanel);
    sidebarToggle.addEventListener('click', toggleSessionSidebar);

    // keyboard shortcuts
    document.addEventListener('keydown', function(e) {
        // '/' focuses search (unless already in input)
        if (e.key === '/' && document.activeElement !== searchInput) {
            e.preventDefault();
            searchInput.focus();
        }

        // Escape clears search
        if (e.key === 'Escape') {
            searchInput.value = '';
            searchInput.blur();
            handleSearch();
        }

        // 'P' toggles plan panel (unless in input)
        if ((e.key === 'p' || e.key === 'P') && document.activeElement !== searchInput) {
            e.preventDefault();
            togglePlanPanel();
        }

        // 'S' toggles session sidebar (unless in input)
        if ((e.key === 's' || e.key === 'S') && document.activeElement !== searchInput) {
            e.preventDefault();
            toggleSessionSidebar();
        }
    });

    // scroll tracking
    outputPanel.addEventListener('scroll', function() {
        checkScroll();
        handleManualScroll();
    });

    scrollToBottomBtn.addEventListener('click', scrollToBottom);

    // cleanup on page unload to prevent memory leaks
    window.addEventListener('beforeunload', function() {
        // save scroll position before leaving
        saveScrollPosition(state.currentSessionId);

        if (state.elapsedTimerInterval) {
            clearInterval(state.elapsedTimerInterval);
            state.elapsedTimerInterval = null;
        }
        if (state.searchTimeout) {
            clearTimeout(state.searchTimeout);
            state.searchTimeout = null;
        }
        if (state.currentEventSource) {
            state.currentEventSource.close();
            state.currentEventSource = null;
        }
        if (state.sessionPollInterval) {
            clearInterval(state.sessionPollInterval);
            state.sessionPollInterval = null;
        }
    });

    // listen for hash changes to switch sessions
    window.addEventListener('hashchange', function() {
        var newId = window.location.hash.slice(1);
        if (newId && newId !== state.currentSessionId) {
            selectSession(newId);
        }
    });

    // get export CSS styles for standalone HTML export.
    // MAINTENANCE: this minified CSS must be kept in sync with styles.css.
    // when updating styles.css, regenerate this by minifying the CSS and updating here.
    // the export feature creates offline-viewable HTML files that don't require serving.
    function getExportCss() {
        return ':root{--bg-primary:#0d1117;--bg-secondary:#161b22;--bg-tertiary:#21262d;--text-primary:#e6edf3;--text-secondary:#8b949e;--text-muted:#484f58;--border-color:#30363d;--phase-task:#3fb950;--phase-review:#58a6ff;--phase-codex:#d2a8ff;--color-error:#f85149;--color-warn:#d29922;--color-section:#ffa657;--color-timestamp:#6e7681}\n' +
            '*{box-sizing:border-box;margin:0;padding:0}\n' +
            'html,body{height:100%;overflow:hidden}\n' +
            'body{font-family:ui-monospace,SFMono-Regular,"SF Mono",Menlo,Consolas,monospace;font-size:13px;line-height:1.5;background:var(--bg-primary);color:var(--text-primary);display:flex;flex-direction:column}\n' +
            'header{background:var(--bg-secondary);border-bottom:1px solid var(--border-color);padding:12px 20px;flex-shrink:0}\n' +
            '.header-top{display:flex;justify-content:space-between;align-items:center;margin-bottom:8px}\n' +
            'header h1{font-size:16px;font-weight:600;color:var(--phase-task);margin:0}\n' +
            '.status-area{display:flex;align-items:center;gap:12px}\n' +
            '.elapsed-time{font-size:12px;color:var(--text-primary);font-weight:500}\n' +
            '.status-badge{font-size:11px;font-weight:600;padding:4px 10px;border-radius:4px;background:var(--bg-tertiary);color:var(--text-secondary);text-transform:uppercase}\n' +
            '.status-badge.task{background:rgba(63,185,80,0.15);color:var(--phase-task);border:1px solid var(--phase-task)}\n' +
            '.status-badge.review{background:rgba(88,166,255,0.15);color:var(--phase-review);border:1px solid var(--phase-review)}\n' +
            '.status-badge.codex{background:rgba(210,168,255,0.15);color:var(--phase-codex);border:1px solid var(--phase-codex)}\n' +
            '.status-badge.completed{background:rgba(63,185,80,0.15);color:var(--phase-task);border:1px solid var(--phase-task)}\n' +
            '.info{display:flex;gap:20px;font-size:12px;color:var(--text-secondary)}\n' +
            '.info span::before{color:var(--text-muted);margin-right:4px}\n' +
            '.plan::before{content:"Plan:"}.branch::before{content:"Branch:"}\n' +
            '.phase-nav{display:flex;gap:4px;padding:8px 20px;background:var(--bg-secondary);border-bottom:1px solid var(--border-color);flex-shrink:0;align-items:center}\n' +
            '.phase-tab,.collapse-btn{font-family:inherit;font-size:12px;padding:6px 12px;border:1px solid var(--border-color);border-radius:6px;background:var(--bg-tertiary);color:var(--text-secondary);cursor:pointer}\n' +
            '.phase-tab:hover,.collapse-btn:hover{background:var(--border-color);color:var(--text-primary)}\n' +
            '.phase-tab.active{background:var(--bg-primary);color:var(--text-primary);border-color:var(--text-muted)}\n' +
            '.phase-tab[data-phase="task"].active{color:var(--phase-task);border-color:var(--phase-task)}\n' +
            '.phase-tab[data-phase="review"].active{color:var(--phase-review);border-color:var(--phase-review)}\n' +
            '.phase-tab[data-phase="codex"].active{color:var(--phase-codex);border-color:var(--phase-codex)}\n' +
            '.nav-separator{width:1px;height:20px;background:var(--border-color);margin:0 8px}\n' +
            '.collapse-btn{font-size:11px;padding:4px 8px;color:var(--text-muted)}\n' +
            '.search-bar{display:flex;align-items:center;gap:12px;padding:8px 20px;background:var(--bg-secondary);border-bottom:1px solid var(--border-color);flex-shrink:0}\n' +
            '#search{flex:1;max-width:400px;font-family:inherit;font-size:13px;padding:6px 12px;border:1px solid var(--border-color);border-radius:6px;background:var(--bg-tertiary);color:var(--text-primary);outline:none}\n' +
            '#search:focus{border-color:var(--phase-review)}\n' +
            '.search-hint{font-size:11px;color:var(--text-muted)}\n' +
            '.main-container{flex:1;display:grid;grid-template-columns:300px 1fr;overflow:hidden}\n' +
            '.main-container.plan-collapsed{grid-template-columns:0 1fr}\n' +
            '.plan-panel{background:var(--bg-secondary);border-right:1px solid var(--border-color);display:flex;flex-direction:column;overflow:hidden}\n' +
            '.main-container.plan-collapsed .plan-panel{display:none}\n' +
            '.plan-panel-header{display:flex;justify-content:space-between;align-items:center;padding:12px 16px;border-bottom:1px solid var(--border-color);flex-shrink:0}\n' +
            '.plan-panel-title{font-weight:600;color:var(--text-primary)}\n' +
            '.plan-toggle{font-family:inherit;font-size:12px;padding:4px 8px;border:1px solid var(--border-color);border-radius:4px;background:var(--bg-tertiary);color:var(--text-secondary);cursor:pointer}\n' +
            '.plan-content{flex:1;overflow-y:auto;padding:12px 16px}\n' +
            '.output-panel{overflow-y:auto;padding:16px 20px}\n' +
            '#output{display:flex;flex-direction:column;gap:2px}\n' +
            '.output-line{display:flex;gap:12px;padding:2px 4px;border-radius:3px}\n' +
            '.output-line:hover{background:var(--bg-secondary)}\n' +
            '.output-line.hidden,.section-header.hidden{display:none}\n' +
            '.timestamp{color:var(--color-timestamp);flex-shrink:0;font-size:12px}\n' +
            '.content{flex:1;white-space:pre-wrap;word-break:break-word}\n' +
            '.output-line[data-phase="task"] .content{color:var(--phase-task)}\n' +
            '.output-line[data-phase="review"] .content{color:var(--phase-review)}\n' +
            '.output-line[data-phase="codex"] .content{color:var(--phase-codex)}\n' +
            '.output-line[data-type="error"] .content{color:var(--color-error)}\n' +
            '.output-line[data-type="warn"] .content{color:var(--color-warn)}\n' +
            '.output-line[data-type="signal"] .content{color:#ff7b72;font-weight:600}\n' +
            '.section-header{margin-top:16px;margin-bottom:8px}\n' +
            '.section-header summary{display:flex;align-items:center;gap:8px;padding:8px 12px;background:var(--bg-secondary);border:1px solid var(--border-color);border-radius:6px;cursor:pointer;list-style:none;color:var(--color-section);font-weight:600}\n' +
            '.section-header summary::-webkit-details-marker{display:none}\n' +
            '.section-header summary::before{content:"▶";font-size:10px;transition:transform 0.15s}\n' +
            '.section-header[open] summary::before{transform:rotate(90deg)}\n' +
            '.section-phase{font-size:11px;padding:2px 6px;border-radius:4px;background:var(--bg-tertiary);font-weight:normal}\n' +
            '.section-header[data-phase="task"] .section-phase{color:var(--phase-task);border:1px solid var(--phase-task)}\n' +
            '.section-header[data-phase="review"] .section-phase{color:var(--phase-review);border:1px solid var(--phase-review)}\n' +
            '.section-header[data-phase="codex"] .section-phase{color:var(--phase-codex);border:1px solid var(--phase-codex)}\n' +
            '.section-duration{margin-left:auto;font-size:11px;font-weight:normal;color:var(--text-secondary)}\n' +
            '.section-content{padding:8px 0 8px 20px;border-left:2px solid var(--border-color);margin-left:6px}\n' +
            '.highlight{background:rgba(210,169,34,0.3);color:var(--color-warn);border-radius:2px;padding:0 2px}\n' +
            '.plan-task{margin-bottom:16px;padding-bottom:12px;border-bottom:1px solid var(--border-color)}\n' +
            '.plan-task:last-child{border-bottom:none}\n' +
            '.plan-task-header{display:flex;align-items:center;gap:8px;margin-bottom:8px;font-weight:600;font-size:12px}\n' +
            '.plan-task-status{width:16px;text-align:center}\n' +
            '.plan-task-status.pending{color:var(--text-muted)}.plan-task-status.done{color:var(--phase-task)}\n' +
            '.plan-task.active{border-left:3px solid var(--phase-review);padding-left:12px;margin-left:-12px}\n' +
            '.plan-checkbox{display:flex;align-items:flex-start;gap:8px;padding:4px 0 4px 24px;font-size:12px;color:var(--text-secondary)}\n' +
            '.plan-checkbox.checked .plan-checkbox-text{text-decoration:line-through;opacity:0.6}\n' +
            '.plan-checkbox-icon.checked{color:var(--phase-task)}\n' +
            '@media(max-width:768px){.main-container{grid-template-columns:1fr}.plan-panel{display:none}}\n';
    }

    // get export JavaScript for standalone HTML export.
    // MAINTENANCE: this minified JS provides basic filtering/search in exported HTML.
    // it's a simplified version of the main app logic - update if core filtering changes.
    // the export feature creates offline-viewable HTML files that don't require serving.
    function getExportJs() {
        return '(function(){var output=document.getElementById("output");var searchInput=document.getElementById("search");var phaseTabs=document.querySelectorAll(".phase-tab");var mainContainer=document.querySelector(".main-container");var planToggle=document.getElementById("plan-toggle");var expandAllBtn=document.getElementById("expand-all");var collapseAllBtn=document.getElementById("collapse-all");var currentPhase="all";var searchTerm="";function escapeRegex(s){return s.replace(/[.*+?^${}()|[\\]\\\\]/g,"\\\\$&")}function setHighlight(el,text,term){el.textContent="";if(!term){el.textContent=text;return}try{var re=new RegExp("("+escapeRegex(term)+")","gi");var parts=text.split(re);parts.forEach(function(p){if(p.toLowerCase()===term.toLowerCase()){var h=document.createElement("span");h.className="highlight";h.textContent=p;el.appendChild(h)}else if(p){el.appendChild(document.createTextNode(p))}})}catch(e){el.textContent=text}}function applyFilters(){var sections=output.querySelectorAll(".section-header");sections.forEach(function(sec){var ph=sec.dataset.phase;var phMatch=currentPhase==="all"||ph===currentPhase;var hasSearch=!searchTerm;if(searchTerm){sec.querySelectorAll(".output-line").forEach(function(ln){var c=ln.querySelector(".content");var t=c.dataset.originalText||c.textContent;if(t.toLowerCase().includes(searchTerm.toLowerCase()))hasSearch=true})}if(phMatch&&hasSearch){sec.classList.remove("hidden")}else{sec.classList.add("hidden")}});output.querySelectorAll(".output-line").forEach(function(ln){var ph=ln.dataset.phase;var c=ln.querySelector(".content");var t=c.dataset.originalText||c.textContent;var phMatch=currentPhase==="all"||ph===currentPhase;var sMatch=!searchTerm||t.toLowerCase().includes(searchTerm.toLowerCase());if(phMatch&&sMatch){ln.classList.remove("hidden")}else{ln.classList.add("hidden")}setHighlight(c,t,searchTerm)})}phaseTabs.forEach(function(tab){tab.addEventListener("click",function(){currentPhase=tab.dataset.phase;phaseTabs.forEach(function(t){t.classList.toggle("active",t.dataset.phase===currentPhase)});applyFilters()})});searchInput.addEventListener("input",function(){searchTerm=searchInput.value.trim();applyFilters()});planToggle.addEventListener("click",function(){mainContainer.classList.toggle("plan-collapsed");planToggle.textContent=mainContainer.classList.contains("plan-collapsed")?"▶":"◀"});expandAllBtn.addEventListener("click",function(){output.querySelectorAll(".section-header").forEach(function(s){s.open=true})});collapseAllBtn.addEventListener("click",function(){output.querySelectorAll(".section-header").forEach(function(s){s.open=false})});document.addEventListener("keydown",function(e){if(e.key==="/"&&document.activeElement!==searchInput){e.preventDefault();searchInput.focus()}if(e.key==="Escape"){searchInput.value="";searchTerm="";searchInput.blur();applyFilters()}if((e.key==="p"||e.key==="P")&&document.activeElement!==searchInput){e.preventDefault();mainContainer.classList.toggle("plan-collapsed");planToggle.textContent=mainContainer.classList.contains("plan-collapsed")?"▶":"◀"}})})();';
    }

    // collect session data for export
    function collectSessionData() {
        const planNameEl = document.querySelector('.plan');
        const branchEl = document.querySelector('.branch');

        return {
            title: document.title,
            planName: planNameEl ? planNameEl.textContent : 'session',
            branch: branchEl ? branchEl.textContent : '',
            elapsed: elapsedTimeEl.textContent || '',
            status: statusBadge.textContent || '',
            statusClass: statusBadge.className.replace('status-badge', '').trim()
        };
    }

    // clone DOM content for export (removes hidden class for full export)
    function cloneContentForExport() {
        const outputClone = output.cloneNode(true);
        outputClone.querySelectorAll('.hidden').forEach(function(el) {
            el.classList.remove('hidden');
        });
        return {
            output: outputClone,
            plan: planContent.cloneNode(true)
        };
    }

    // build export HTML head section
    function buildExportHead(safeTitle) {
        return '<!DOCTYPE html>\n<html lang="en">\n<head>\n' +
            '<meta charset="UTF-8">\n' +
            '<meta name="viewport" content="width=device-width, initial-scale=1.0">\n' +
            '<title>' + safeTitle + ' - Export</title>\n' +
            '<style>\n' + getExportCss() + '</style>\n</head>\n';
    }

    // build export HTML header section
    function buildExportHeader(safeElapsed, safeStatus, safeStatusClass, safePlanName, safeBranch) {
        return '<header>\n' +
            '<div class="header-top">\n' +
            '<h1>Ralphex Dashboard</h1>\n' +
            '<div class="status-area">\n' +
            '<span class="elapsed-time">' + safeElapsed + '</span>\n' +
            '<span class="status-badge ' + safeStatusClass + '">' + safeStatus + '</span>\n' +
            '</div>\n</div>\n' +
            '<div class="info">\n' +
            '<span class="plan">' + safePlanName + '</span>\n' +
            '<span class="branch">' + safeBranch + '</span>\n' +
            '</div>\n</header>\n';
    }

    // build export HTML navigation section
    function buildExportNav() {
        return '<nav class="phase-nav">\n' +
            '<button class="phase-tab active" data-phase="all">All</button>\n' +
            '<button class="phase-tab" data-phase="task">Implementation</button>\n' +
            '<button class="phase-tab" data-phase="review">Claude Review</button>\n' +
            '<button class="phase-tab" data-phase="codex">Codex Review</button>\n' +
            '<span class="nav-separator"></span>\n' +
            '<button class="collapse-btn" id="expand-all">Expand All</button>\n' +
            '<button class="collapse-btn" id="collapse-all">Collapse All</button>\n' +
            '</nav>\n' +
            '<div class="search-bar">\n' +
            '<input type="text" id="search" placeholder="Search... (press / to focus)" autocomplete="off">\n' +
            '<span class="search-hint">Press Escape to clear, P to toggle plan</span>\n' +
            '</div>\n';
    }

    // build export HTML main content section
    function buildExportMain(clones) {
        return '<div class="main-container">\n' +
            '<aside class="plan-panel">\n' +
            '<div class="plan-panel-header">\n' +
            '<span class="plan-panel-title">Plan</span>\n' +
            '<button class="plan-toggle" id="plan-toggle">◀</button>\n' +
            '</div>\n' +
            '<div class="plan-content">\n' + clones.plan.innerHTML + '\n</div>\n' +
            '</aside>\n' +
            '<main class="output-panel">\n' +
            '<div id="output">\n' + clones.output.innerHTML + '\n</div>\n' +
            '</main>\n</div>\n' +
            '<script>\n' + getExportJs() + '\n<\/script>\n' +
            '</body>\n</html>';
    }

    // build export HTML document - uses escapeHtml to prevent XSS from user content
    function buildExportHtml(data, clones) {
        var safeTitle = escapeHtml(data.title);
        var safePlanName = escapeHtml(data.planName);
        var safeBranch = escapeHtml(data.branch);
        var safeElapsed = escapeHtml(data.elapsed);
        var safeStatus = escapeHtml(data.status);
        var safeStatusClass = escapeHtml(data.statusClass);

        return buildExportHead(safeTitle) +
            '<body>\n' +
            buildExportHeader(safeElapsed, safeStatus, safeStatusClass, safePlanName, safeBranch) +
            buildExportNav() +
            buildExportMain(clones);
    }

    // trigger file download
    function downloadFile(content, filename, mimeType) {
        var blob = new Blob([content], { type: mimeType });
        var url = URL.createObjectURL(blob);
        var a = document.createElement('a');
        a.href = url;
        a.download = filename;
        document.body.appendChild(a);
        a.click();
        document.body.removeChild(a);
        URL.revokeObjectURL(url);
    }

    // export session as standalone HTML
    function exportSession() {
        var data = collectSessionData();
        var clones = cloneContentForExport();
        var html = buildExportHtml(data, clones);
        var filename = 'ralphex-' + data.planName.replace(/[^a-z0-9]/gi, '-') + '.html';
        downloadFile(html, filename, 'text/html');
    }

    exportBtn.addEventListener('click', exportSession);

    // expand/collapse all sections
    function expandAllSections() {
        output.querySelectorAll('.section-header').forEach(function(section) {
            section.open = true;
        });
    }

    function collapseAllSections() {
        output.querySelectorAll('.section-header').forEach(function(section) {
            section.open = false;
        });
    }

    expandAllBtn.addEventListener('click', expandAllSections);
    collapseAllBtn.addEventListener('click', collapseAllSections);

    // start
    fetchSessions();
    startSessionPolling();

    // if we have a session ID, fetch its plan; otherwise use server default
    if (state.currentSessionId) {
        fetchPlanForSession(state.currentSessionId);
    } else {
        fetchPlan();
    }
    connect();
})();
