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
    const diffStatsEl = document.getElementById('diff-stats');
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
    const helpOverlay = document.getElementById('help-overlay');
    const helpCloseBtn = document.getElementById('help-close');
    const helpBtn = document.getElementById('help-btn');

    // session sidebar elements
    const sessionSidebar = document.getElementById('session-sidebar');
    const sessionList = document.getElementById('session-list');
    const sidebarToggle = document.getElementById('sidebar-toggle');
    const viewToggle = document.getElementById('view-toggle');
    const mainWrapper = document.getElementById('main-wrapper');
    const projectPathEl = document.getElementById('project-path');
    const projectWrapEl = document.getElementById('project-wrap');
    const projectCopyBtn = document.getElementById('project-copy');
    const planNameEl = document.getElementById('plan-name');
    const branchNameEl = document.getElementById('branch-name');

    // SSE reconnection constants
    var SSE_INITIAL_RECONNECT_MS = 1000;
    var SSE_MAX_RECONNECT_MS = 30000;

    // session polling interval
    var SESSION_POLL_INTERVAL_MS = 5000;

    // view mode constants
    var VIEW_MODE = {
        RECENT: 'recent',
        GROUPED: 'grouped'
    };

    // normalize view mode from localStorage (handles corrupted/invalid values)
    function normalizeViewMode(saved) {
        if (saved === VIEW_MODE.GROUPED || saved === VIEW_MODE.RECENT) {
            return saved;
        }
        return VIEW_MODE.RECENT;
    }

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
        sessionViewMode: normalizeViewMode(localStorage.getItem('sessionViewMode')),
        planData: null,

        // session state
        sessions: [],
        currentSessionId: null,
        currentSession: null,
        sessionPollInterval: null,

        // timing state
        executionStartTime: null,
        lastEventTimestamp: null, // tracks most recent event timestamp for duration calculations
        sectionStartTimes: {},
        elapsedTimerInterval: null,
        sectionCounter: 0, // monotonically increasing counter for unique section IDs
        isTerminalState: false, // true when COMPLETED/FAILED signal received
        seenSections: {}, // track seen sections to avoid duplicates
        currentTaskNum: null, // current active task number from task_start events
        focusedSectionIndex: -1, // for j/k navigation
        focusedSectionElement: null, // direct reference to focused section for O(1) unfocus
        hasRunTerminalCleanup: false, // guard for terminal cleanup to prevent double-calls
        expandedSections: {}, // tracks user-expanded sections per session {sessionId: Set of sectionIds}

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
        document.body.classList.add('plan-collapsed');
    }
    // always set icon explicitly based on state (don't rely on HTML default)
    planToggle.textContent = state.planCollapsed ? '◀' : '▶';

    // initialize sidebar state
    if (state.sidebarCollapsed) {
        document.body.classList.add('sidebar-collapsed');
        sidebarToggle.textContent = '▶';
    }

    // initialize view toggle state
    updateViewToggleButton();

    // load saved section expansion state
    loadExpandedSections();

    // batch size for event queue processing
    var BATCH_SIZE = 100;

    // max sessions to track expansion state for (prevents localStorage bloat)
    var MAX_PERSISTED_SESSIONS = 20;

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
                // for completed sessions, clear active task styling
                if (state.isTerminalState) {
                    runTerminalCleanupOnce();
                }

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

    // regex pattern for task iteration sections (hoisted for performance)
    var TASK_ITERATION_PATTERN = /^task iteration \d+$/i;
    var TASK_ITERATION_NUMBER_PATTERN = /^task iteration (\d+)$/i;
    var DIFF_STATS_PATTERN = /^DIFFSTATS:\s*files=(\d+)\s+additions=(\d+)\s+deletions=(\d+)\s*$/i;

    // check if section text is a task iteration pattern
    function isTaskIteration(sectionText) {
        if (!sectionText) return false;
        return TASK_ITERATION_PATTERN.test(sectionText);
    }

    function extractTaskIterationNumber(sectionText) {
        if (!sectionText) return null;
        var matches = TASK_ITERATION_NUMBER_PATTERN.exec(sectionText);
        if (!matches) return null;
        var num = parseInt(matches[1], 10);
        return isNaN(num) ? null : num;
    }

    function formatDiffStats(stats) {
        if (!stats || !stats.files) return '';
        return stats.files + ' files +' + stats.additions + '/-' + stats.deletions;
    }

    function updateDiffStats(stats) {
        if (!diffStatsEl) return;
        var text = formatDiffStats(stats);
        if (!text) {
            diffStatsEl.textContent = '';
            diffStatsEl.removeAttribute('title');
            return;
        }
        var additions = typeof stats.additions === 'number' ? stats.additions : 0;
        var deletions = typeof stats.deletions === 'number' ? stats.deletions : 0;
        diffStatsEl.textContent = '';
        var filesSpan = document.createElement('span');
        filesSpan.className = 'diff-files';
        filesSpan.textContent = stats.files + ' files ';

        var addSpan = document.createElement('span');
        addSpan.className = 'diff-additions';
        addSpan.textContent = '+' + additions;

        var slashSpan = document.createElement('span');
        slashSpan.className = 'diff-separator';
        slashSpan.textContent = '/';

        var delSpan = document.createElement('span');
        delSpan.className = 'diff-deletions';
        delSpan.textContent = '-' + deletions;

        diffStatsEl.appendChild(filesSpan);
        diffStatsEl.appendChild(addSpan);
        diffStatsEl.appendChild(slashSpan);
        diffStatsEl.appendChild(delSpan);
        diffStatsEl.title = text;
    }

    function parseDiffStatsText(text) {
        if (!text) return null;
        var matches = DIFF_STATS_PATTERN.exec(text);
        if (!matches) return null;
        return {
            files: parseInt(matches[1], 10),
            additions: parseInt(matches[2], 10),
            deletions: parseInt(matches[3], 10)
        };
    }

    // look up task title by position (1-indexed) from plan data
    function getTaskTitle(taskNum) {
        if (!state.planData || !state.planData.tasks) return null;
        var index = taskNum - 1;
        if (index >= 0 && index < state.planData.tasks.length) {
            return state.planData.tasks[index].title;
        }
        return null;
    }

    // format section title for task iterations using current active task
    // stores task number in data attribute for later refresh if plan loads after events
    function formatSectionTitle(sectionText, sectionElement) {
        if (isTaskIteration(sectionText)) {
            var taskNum = state.currentTaskNum || extractTaskIterationNumber(sectionText);
            if (taskNum) {
                // store the task number for later refresh
                if (sectionElement) {
                    sectionElement.dataset.taskNum = taskNum;
                }
                var title = getTaskTitle(taskNum);
                if (title) {
                    return 'Task ' + taskNum + ': ' + title;
                }
                return 'Task ' + taskNum;
            }
        }
        return sectionText;
    }

    // update all section headers with task titles after plan data loads
    // this handles completed sessions where events arrive before plan data
    function refreshSectionTitles() {
        if (!state.planData || !state.planData.tasks) return;
        var sections = output.querySelectorAll('.section-header[data-task-num]');
        sections.forEach(function(section) {
            var titleEl = section.querySelector('.section-title');
            if (!titleEl) return;
            var taskNum = parseInt(section.dataset.taskNum, 10);
            if (taskNum) {
                var title = getTaskTitle(taskNum);
                if (title) {
                    titleEl.textContent = 'Task ' + taskNum + ': ' + title;
                }
            }
        });
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
    // sections start collapsed; only current section in live sessions is expanded
    function createSectionHeader(event) {
        // collapse previous section (it's now "completed")
        if (state.currentSection) {
            state.currentSection.open = false;
            state.currentSection.classList.remove('section-focused');
        }

        state.sectionCounter++;
        var sectionId = 'section-' + state.sectionCounter;

        const details = document.createElement('details');
        details.className = 'section-header';
        details.dataset.phase = event.phase;
        details.dataset.sectionId = sectionId;

        // check if user explicitly expanded this section, or if it's a live session's current section
        var isLive = isLiveSession();
        var userExpanded = isSectionExpanded(sectionId);
        details.open = userExpanded || isLive; // live sessions: current section expanded

        const summary = document.createElement('summary');

        const phaseLabel = document.createElement('span');
        phaseLabel.className = 'section-phase';
        phaseLabel.textContent = event.phase;

        const title = document.createElement('span');
        title.className = 'section-title';
        title.textContent = formatSectionTitle(event.section || event.text, details);

        const duration = document.createElement('span');
        duration.className = 'section-duration';
        duration.textContent = '';

        summary.appendChild(phaseLabel);
        summary.appendChild(title);
        summary.appendChild(duration);

        // track user toggle on click (setTimeout lets browser update details.open first)
        summary.addEventListener('click', function(e) {
            setTimeout(function() {
                trackSectionToggle(sectionId, details.open);
            }, 0);
        });

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

    // check if a section was explicitly expanded by user
    function isSectionExpanded(sectionId) {
        if (!state.currentSessionId) return false;
        var expanded = state.expandedSections[state.currentSessionId];
        return expanded && expanded[sectionId];
    }

    // track user toggle of section expand/collapse
    function trackSectionToggle(sectionId, isOpen) {
        if (!state.currentSessionId) return;

        if (!state.expandedSections[state.currentSessionId]) {
            state.expandedSections[state.currentSessionId] = {};
        }

        if (isOpen) {
            state.expandedSections[state.currentSessionId][sectionId] = true;
        } else {
            delete state.expandedSections[state.currentSessionId][sectionId];
        }

        // persist to localStorage (limit to recent sessions to avoid bloat)
        saveExpandedSections();
    }

    // debounce timer for localStorage writes
    var saveExpandedDebounce = null;

    // save expanded sections to localStorage (debounced to avoid thrashing)
    function saveExpandedSections() {
        if (saveExpandedDebounce) clearTimeout(saveExpandedDebounce);
        saveExpandedDebounce = setTimeout(function() {
            // only keep last N sessions to avoid localStorage bloat
            var keys = Object.keys(state.expandedSections);
            if (keys.length > MAX_PERSISTED_SESSIONS) {
                var toRemove = keys.slice(0, keys.length - MAX_PERSISTED_SESSIONS);
                toRemove.forEach(function(k) { delete state.expandedSections[k]; });
            }
            localStorage.setItem('expandedSections', JSON.stringify(state.expandedSections));
        }, 500);
    }

    // load expanded sections from localStorage
    function loadExpandedSections() {
        try {
            var saved = localStorage.getItem('expandedSections');
            if (saved) {
                var parsed = JSON.parse(saved);
                // validate parsed data is a plain object
                if (parsed && typeof parsed === 'object' && !Array.isArray(parsed)) {
                    // use null-prototype object to prevent prototype pollution
                    state.expandedSections = Object.assign(Object.create(null), parsed);
                    // remove any prototype pollution keys
                    delete state.expandedSections.__proto__;
                    delete state.expandedSections.constructor;
                    delete state.expandedSections.prototype;
                } else {
                    state.expandedSections = {};
                }
            }
        } catch (e) {
            console.error('[localStorage] Failed to parse expandedSections:', e);
            state.expandedSections = {};
        }
    }

    // update section duration display - uses direct selector for O(1) lookup
    // endTimestamp is optional - if provided, uses it instead of calculating from session state
    function updateSectionDuration(sectionId, endTimestamp) {
        var startTime = state.sectionStartTimes[sectionId];
        if (!startTime) return;

        // use provided endTimestamp, or derive from session state
        var endTime = endTimestamp || getElapsedEndTime();
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
            // only COMPLETED (from ALL_TASKS_DONE) is a terminal success signal
            // REVIEW_DONE and CODEX_REVIEW_DONE mark end of review passes, not end of execution
            var isSuccess = event.signal === 'COMPLETED';
            var isFailed = event.signal === 'FAILED';

            if (isSuccess || isFailed) {
                statusBadge.textContent = isSuccess ? 'COMPLETED' : 'FAILED';
                statusBadge.classList.add(isSuccess ? 'completed' : 'failed');
                state.isTerminalState = true;
                updateTimers();
                if (state.elapsedTimerInterval) {
                    clearInterval(state.elapsedTimerInterval);
                    state.elapsedTimerInterval = null;
                }
                runTerminalCleanupOnce();
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

    function getSelectedSessionFromList() {
        if (!state.currentSessionId || !state.sessions || state.sessions.length === 0) {
            return state.currentSession;
        }
        for (var i = 0; i < state.sessions.length; i++) {
            if (state.sessions[i].id === state.currentSessionId) {
                return state.sessions[i];
            }
        }
        return state.currentSession;
    }

    // check if current session is live (active or optimistic when state is unknown).
    // does not check isTerminalState — callers decide terminal handling separately.
    function isLiveSession() {
        var session = getSelectedSessionFromList();
        // prefer session state when available
        if (session && session.state) {
            return session.state === 'active';
        }
        // fallback for single-session mode or before sessions load:
        // consider live only if we have a start time and recent events
        if (state.executionStartTime && state.lastEventTimestamp) {
            return (Date.now() - state.lastEventTimestamp) < 60000;
        }
        return false;
    }

    function getElapsedEndTime() {
        var session = getSelectedSessionFromList();
        if (isLiveSession()) {
            return Date.now();
        }
        if (state.lastEventTimestamp) {
            return state.lastEventTimestamp;
        }
        if (session && session.lastModified) {
            var lastModified = parseTimeMs(session.lastModified);
            if (Number.isFinite(lastModified) && lastModified > 0) {
                return lastModified;
            }
        }
        return Date.now();
    }

    // update elapsed time display and current section duration
    function updateTimers() {
        if (!state.executionStartTime) return;
        // optimistic for live sessions; otherwise use last known timestamp
        var endTime = getElapsedEndTime();
        var elapsed = endTime - state.executionStartTime;
        elapsedTimeEl.textContent = formatDuration(elapsed);

        // update current section duration
        if (state.currentSection) {
            var sectionId = state.currentSection.dataset.sectionId;
            updateSectionDuration(sectionId);
        }
    }

    function shouldRunElapsedTimer() {
        return !state.isTerminalState && isLiveSession();
    }

    // start elapsed time timer - clears any existing interval to prevent memory leaks on reconnect
    function startElapsedTimer() {
        if (state.elapsedTimerInterval) {
            clearInterval(state.elapsedTimerInterval);
            state.elapsedTimerInterval = null;
        }
        if (!shouldRunElapsedTimer()) return;
        state.elapsedTimerInterval = setInterval(updateTimers, 1000);
    }

    // handle task boundary events.
    // if target task element not found (plan was edited mid-run), re-fetch plan and retry.
    function handleTaskStart(event) {
        state.currentTaskNum = event.task_num;
        clearActiveTasksExcept(event.task_num);

        var taskEl = planContent.querySelector('.plan-task[data-task-num="' + event.task_num + '"]');
        if (taskEl) {
            updatePlanTaskStatus(event.task_num, 'active');
            return;
        }

        // task element not found - plan may have been edited mid-run, re-fetch
        var url = '/api/plan';
        if (state.currentSessionId) {
            url += '?session=' + encodeURIComponent(state.currentSessionId);
        }
        var taskNum = event.task_num;
        fetch(url)
            .then(function(response) {
                if (!response.ok) throw new Error('plan not available');
                return response.json();
            })
            .then(function(plan) {
                state.planData = plan;
                renderPlan(plan);
                // only set active if task_end hasn't arrived during fetch
                if (state.currentTaskNum === taskNum) {
                    updatePlanTaskStatus(taskNum, 'active');
                }
            })
            .catch(function(err) {
                console.log('plan re-fetch failed:', err.message);
            });
    }

    function handleTaskEnd(event) {
        state.currentTaskNum = null;
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

    // ensure only one task is highlighted as active
    function clearActiveTasksExcept(taskNum) {
        var activeTasks = planContent.querySelectorAll('.plan-task.active');
        activeTasks.forEach(function(taskEl) {
            var num = parseInt(taskEl.dataset.taskNum, 10);
            if (num === taskNum) {
                return;
            }
            taskEl.classList.remove('active');
            var statusEl = taskEl.querySelector('.plan-task-status');
            if (!statusEl) {
                return;
            }
            statusEl.classList.remove('active', 'pending', 'done', 'failed');
            var unchecked = taskEl.querySelectorAll('.plan-checkbox:not(.checked)');
            if (unchecked.length === 0) {
                statusEl.classList.add('done');
                statusEl.textContent = '✓';
            } else {
                statusEl.classList.add('pending');
                statusEl.textContent = '○';
            }
        });
    }

    // render event to output
    function renderEvent(event) {
        var eventTimestamp = new Date(event.timestamp).getTime();

        // track execution start time
        if (!state.executionStartTime || eventTimestamp < state.executionStartTime) {
            state.executionStartTime = eventTimestamp;
        }
        if (state.executionStartTime && !state.elapsedTimerInterval && shouldRunElapsedTimer()) {
            startElapsedTimer();
        }

        // always update lastEventTimestamp for duration calculations
        state.lastEventTimestamp = eventTimestamp;

        if (event && event.type === 'output') {
            var diffStats = parseDiffStatsText(event.text);
            if (diffStats) {
                if (state.currentSession) {
                    state.currentSession.diffStats = diffStats;
                }
                updateDiffStats(diffStats);
                return; // metadata line, don't render
            }
        }

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
        // reset focused section when filters change
        state.focusedSectionIndex = -1;
        if (state.focusedSectionElement) {
            state.focusedSectionElement.classList.remove('section-focused');
            state.focusedSectionElement = null;
        }
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
            document.body.classList.add('plan-collapsed');
            planToggle.textContent = '◀';
        } else {
            mainContainer.classList.remove('plan-collapsed');
            document.body.classList.remove('plan-collapsed');
            planToggle.textContent = '▶';
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

    // toggle session view mode (recent vs grouped by project)
    function toggleSessionViewMode() {
        setSessionViewMode(state.sessionViewMode === VIEW_MODE.RECENT ? VIEW_MODE.GROUPED : VIEW_MODE.RECENT);
    }

    // set session view mode to specific value
    function setSessionViewMode(mode) {
        if (state.sessionViewMode === mode) return;
        state.sessionViewMode = mode;
        localStorage.setItem('sessionViewMode', state.sessionViewMode);
        updateViewToggleButton();
        renderSessionList(state.sessions);
    }

    // update view toggle button appearance
    function updateViewToggleButton() {
        if (!viewToggle) return;
        var icon = viewToggle.querySelector('.view-icon');
        if (state.sessionViewMode === VIEW_MODE.GROUPED) {
            viewToggle.classList.add('grouped');
            viewToggle.title = 'Grouped by project (t for recent)';
            if (icon) icon.textContent = 'G';
        } else {
            viewToggle.classList.remove('grouped');
            viewToggle.title = 'Sorted by time (g to group)';
            if (icon) icon.textContent = 'T';
        }
    }

    // get visible sections (respects phase filter)
    function getVisibleSections() {
        var all = output.querySelectorAll('.section-header');
        var visible = [];
        all.forEach(function(section) {
            if (!section.classList.contains('hidden')) {
                visible.push(section);
            }
        });
        return visible;
    }

    // navigate to adjacent section (direction: 1 for next, -1 for previous)
    function navigateSection(direction) {
        var sections = getVisibleSections();
        if (sections.length === 0) return;

        state.focusedSectionIndex += direction;
        state.focusedSectionIndex = Math.max(0, Math.min(state.focusedSectionIndex, sections.length - 1));

        focusSection(sections[state.focusedSectionIndex]);
    }

    // focus a section: scroll into view and highlight (does NOT change expand state)
    function focusSection(section) {
        if (!section) return;

        // remove focus from previously focused section (O(1) instead of querying all)
        if (state.focusedSectionElement) {
            state.focusedSectionElement.classList.remove('section-focused');
        }

        // focus this section and track it
        section.classList.add('section-focused');
        state.focusedSectionElement = section;

        // scroll into view with some padding
        section.scrollIntoView({ behavior: 'smooth', block: 'start' });

        // disable auto-scroll when navigating manually
        state.autoScroll = false;
    }

    // toggle expand/collapse of currently focused section
    function toggleFocusedSection() {
        var sections = getVisibleSections();
        if (state.focusedSectionIndex < 0 || state.focusedSectionIndex >= sections.length) return;

        var section = sections[state.focusedSectionIndex];
        if (section) {
            section.open = !section.open;
            var sectionId = section.dataset.sectionId;
            if (sectionId) {
                trackSectionToggle(sectionId, section.open);
            }
        }
    }

    // help modal controls
    function showHelp() { if (helpOverlay) helpOverlay.classList.add('visible'); }
    function hideHelp() { if (helpOverlay) helpOverlay.classList.remove('visible'); }
    function isHelpVisible() { return helpOverlay && helpOverlay.classList.contains('visible'); }

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

    function parseTimeMs(value) {
        if (!value) return null;
        var ts = new Date(value).getTime();
        if (!isFinite(ts) || ts <= 0) return null;
        return ts;
    }

    function seedExecutionStartTimeFromSession(session) {
        var startMs = session ? parseTimeMs(session.startTime) : null;
        if (!startMs) return;
        if (!state.executionStartTime || startMs < state.executionStartTime) {
            state.executionStartTime = startMs;
        }
        if (state.executionStartTime && !state.elapsedTimerInterval && shouldRunElapsedTimer()) {
            startElapsedTimer();
        }
        updateTimers();
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

        if (state.sessionViewMode === VIEW_MODE.GROUPED) {
            renderSessionsGrouped(sessions);
        } else {
            renderSessionsRecent(sessions);
        }
    }

    // render sessions as flat list sorted by recency
    function renderSessionsRecent(sessions) {
        sessions.forEach(function(session) {
            sessionList.appendChild(createSessionItem(session, true)); // show project in flat list
        });
    }

    // render sessions grouped by project directory
    function renderSessionsGrouped(sessions) {
        // group sessions by directory
        var groups = {};
        sessions.forEach(function(session) {
            var dir = session.dirPath || session.dir || 'Unknown';
            if (!groups[dir]) {
                groups[dir] = [];
            }
            groups[dir].push(session);
        });

        // sort groups by most recent session in each group
        var sortedDirs = Object.keys(groups).sort(function(a, b) {
            var aLatest = new Date(groups[a][0].lastModified).getTime();
            var bLatest = new Date(groups[b][0].lastModified).getTime();
            return bLatest - aLatest;
        });

        // render each group
        sortedDirs.forEach(function(dir) {
            var group = document.createElement('div');
            group.className = 'project-group';

            // group header
            var header = document.createElement('div');
            header.className = 'project-group-header';

            var icon = document.createElement('span');
            icon.className = 'group-icon';
            icon.textContent = '▼';

            var name = document.createElement('span');
            name.className = 'group-name';
            name.textContent = extractProjectName(dir);
            name.title = dir;

            var count = document.createElement('span');
            count.className = 'group-count';
            count.textContent = '(' + groups[dir].length + ')';

            header.appendChild(icon);
            header.appendChild(name);
            header.appendChild(count);

            // toggle group collapse
            header.addEventListener('click', function() {
                group.classList.toggle('collapsed');
            });

            // sessions container
            var sessionsContainer = document.createElement('div');
            sessionsContainer.className = 'project-group-sessions';

            groups[dir].forEach(function(session) {
                sessionsContainer.appendChild(createSessionItem(session));
            });

            group.appendChild(header);
            group.appendChild(sessionsContainer);
            sessionList.appendChild(group);
        });
    }

    // extract short project name from full path
    function extractProjectName(dir) {
        if (!dir) return 'Unknown';
        var parts = dir.split(/[\\/]/);
        // return last non-empty part
        for (var i = parts.length - 1; i >= 0; i--) {
            if (parts[i]) return parts[i];
        }
        return dir;
    }

    // create a session item element
    // showProject: if true, show project badge (used in time-sorted view)
    function createSessionItem(session, showProject) {
        var item = document.createElement('div');
        item.className = 'session-item';
        item.dataset.sessionId = session.id;

        if (session.id === state.currentSessionId) {
            item.classList.add('selected');
        }

        // session info container
        var info = document.createElement('div');
        info.className = 'session-info';

        // top row: indicator + plan name
        var topRow = document.createElement('div');
        topRow.className = 'session-row session-row-top';

        var indicator = document.createElement('span');
        indicator.className = 'session-indicator';
        if (session.state === 'active') {
            indicator.classList.add('active');
            indicator.title = 'Active session';
        } else {
            indicator.classList.add('completed');
            indicator.title = 'Completed session';
        }

        var name = document.createElement('div');
        name.className = 'session-name';
        name.textContent = extractPlanName(session.planPath);

        topRow.appendChild(indicator);
        topRow.appendChild(name);

        var timeSpan = document.createElement('span');
        timeSpan.className = 'session-time session-time-top';
        timeSpan.textContent = formatRelativeTime(session.lastModified);

        topRow.appendChild(timeSpan);

        info.appendChild(topRow);
        var projectFullPath = session.dirPath || session.dir || '';
        if (showProject && projectFullPath) {
            // second row: project
            var metaRow = document.createElement('div');
            metaRow.className = 'session-row session-row-meta';

            var projectSpan = document.createElement('span');
            projectSpan.className = 'session-project';
            projectSpan.textContent = extractProjectName(projectFullPath);
            projectSpan.title = projectFullPath;
            metaRow.appendChild(projectSpan);
            info.appendChild(metaRow);
        }

        if (session.branch) {
            var branchRow = document.createElement('div');
            branchRow.className = 'session-row session-row-branch';

            var branchSpan = document.createElement('span');
            branchSpan.className = 'session-branch';
            branchSpan.textContent = session.branch;

            branchRow.appendChild(branchSpan);
            info.appendChild(branchRow);
        }

        item.appendChild(info);

        // click handler
        item.addEventListener('click', function() {
            selectSession(session.id);
        });

        return item;
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
            if (projectPathEl) {
                var fullPath = session.dirPath || session.dir || '';
                projectPathEl.textContent = fullPath ? extractProjectName(fullPath) : '';
                projectPathEl.title = fullPath;
                projectPathEl.dataset.fullPath = fullPath;
                if (projectWrapEl) {
                    projectWrapEl.classList.toggle('is-hidden', !fullPath);
                }
            }
            if (planNameEl) {
                planNameEl.textContent = extractPlanName(session.planPath);
            }
            if (branchNameEl) {
                branchNameEl.textContent = session.branch || '';
            }
            state.currentSession = session;
            updateDiffStats(session.diffStats);
            seedExecutionStartTimeFromSession(session);
        }

        // reconnect SSE to new session
        reconnectToSession(sessionId);

        // reload plan for new session
        fetchPlanForSession(sessionId);
    }

    function copyTextToClipboard(text) {
        if (!text) return Promise.resolve(false);
        if (navigator.clipboard && window.isSecureContext) {
            return navigator.clipboard.writeText(text).then(function() { return true; });
        }
        var textarea = document.createElement('textarea');
        textarea.value = text;
        textarea.setAttribute('readonly', '');
        textarea.style.position = 'fixed';
        textarea.style.top = '-9999px';
        document.body.appendChild(textarea);
        textarea.select();
        try {
            var ok = document.execCommand('copy');
            return Promise.resolve(ok);
        } catch (err) {
            return Promise.resolve(false);
        } finally {
            document.body.removeChild(textarea);
        }
    }

    // reconnect SSE stream to a specific session
    function reconnectToSession(sessionId) {
        // close existing connection
        if (state.currentEventSource) {
            state.currentEventSource.close();
            state.currentEventSource = null;
        }

        // reset output state
        var sessionStartTime = state.currentSession && state.currentSession.startTime;
        resetOutputState({ seedStartTime: sessionStartTime });
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

    function resetOutputState(options) {
        var seedStartTime = options && options.seedStartTime;
        clearElement(output);
        state.currentSection = null;
        state.sectionStartTimes = {};
        state.sectionCounter = 0;
        state.executionStartTime = null;
        state.lastEventTimestamp = null;
        state.isTerminalState = false;
        state.hasRunTerminalCleanup = false;
        state.seenSections = {};
        state.currentTaskNum = null;
        state.eventQueue = [];
        state.isProcessingQueue = false;
        state.focusedSectionIndex = -1;
        state.focusedSectionElement = null;
        if (state.elapsedTimerInterval) {
            clearInterval(state.elapsedTimerInterval);
            state.elapsedTimerInterval = null;
        }
        elapsedTimeEl.textContent = '';
        updateDiffStats(null);
        if (seedStartTime) {
            seedExecutionStartTimeFromSession({ startTime: seedStartTime });
        }
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

        plan.tasks.forEach(function(task, index) {
            const taskEl = document.createElement('div');
            taskEl.className = 'plan-task';
            taskEl.dataset.taskNum = index + 1;

            var displayStatus = task.status;
            if (displayStatus === 'active') {
                var allChecked = task.checkboxes && task.checkboxes.length > 0 &&
                    task.checkboxes.every(function(cb) { return cb.checked; });
                displayStatus = allChecked ? 'done' : 'pending';
            }

            const header = document.createElement('div');
            header.className = 'plan-task-header';

            const statusIcon = document.createElement('span');
            statusIcon.className = 'plan-task-status ' + displayStatus;
            switch (displayStatus) {
                case 'pending': statusIcon.textContent = '○'; break;
                case 'active': statusIcon.textContent = '●'; break;
                case 'done': statusIcon.textContent = '✓'; break;
                case 'failed': statusIcon.textContent = '✗'; break;
                default: statusIcon.textContent = '○';
            }

            const title = document.createElement('span');
            title.className = 'plan-task-title';
            var displayNum = task.number > 0 ? task.number : (index + 1);
            title.textContent = 'Task ' + displayNum + ': ' + task.title;

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

        // update any section headers that were rendered before plan data loaded
        refreshSectionTitles();
        if (state.currentTaskNum) {
            updatePlanTaskStatus(state.currentTaskNum, 'active');
        }
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
    if (viewToggle) {
        viewToggle.addEventListener('click', toggleSessionViewMode);
    }
    if (projectCopyBtn && projectPathEl) {
        var copyResetTimer = null;
        var copyLabel = projectCopyBtn.textContent;
        projectCopyBtn.addEventListener('click', function() {
            var fullPath = projectPathEl.dataset.fullPath || projectPathEl.title || projectPathEl.textContent;
            copyTextToClipboard(fullPath).then(function(success) {
                if (!success) return;
                projectCopyBtn.textContent = 'Copied';
                projectCopyBtn.classList.add('copied');
                if (copyResetTimer) {
                    clearTimeout(copyResetTimer);
                }
                copyResetTimer = setTimeout(function() {
                    projectCopyBtn.textContent = copyLabel;
                    projectCopyBtn.classList.remove('copied');
                }, 1200);
            });
        });
    }

    // keyboard shortcuts
    document.addEventListener('keydown', function(e) {
        // '?' shows help (unless in input)
        if (e.key === '?' && document.activeElement !== searchInput) {
            e.preventDefault();
            showHelp();
            return;
        }

        // Escape closes help, or clears search
        if (e.key === 'Escape') {
            if (isHelpVisible()) {
                hideHelp();
                return;
            }
            searchInput.value = '';
            searchInput.blur();
            handleSearch();
            return;
        }

        // ignore other shortcuts when help is visible
        if (isHelpVisible()) return;

        // '/' focuses search (unless already in input)
        if (e.key === '/' && document.activeElement !== searchInput) {
            e.preventDefault();
            searchInput.focus();
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

        // 't' switches to time-sorted view (unless in input)
        if (e.key === 't' && document.activeElement !== searchInput) {
            e.preventDefault();
            setSessionViewMode(VIEW_MODE.RECENT);
        }

        // 'g' switches to grouped-by-project view (unless in input)
        if (e.key === 'g' && document.activeElement !== searchInput) {
            e.preventDefault();
            setSessionViewMode(VIEW_MODE.GROUPED);
        }

        // 'j'/'k' navigate between sections (unless in input)
        if (e.key === 'j' && document.activeElement !== searchInput) {
            e.preventDefault();
            navigateSection(1);
        }

        if (e.key === 'k' && document.activeElement !== searchInput) {
            e.preventDefault();
            navigateSection(-1);
        }

        // 'e' expands all sections (unless in input)
        if (e.key === 'e' && document.activeElement !== searchInput) {
            e.preventDefault();
            expandAllSections();
        }

        // 'c' collapses all sections (unless in input)
        if (e.key === 'c' && document.activeElement !== searchInput) {
            e.preventDefault();
            collapseAllSections();
        }

        // Space or Enter toggles focused section expand/collapse (unless in input)
        if ((e.key === ' ' || e.key === 'Enter') && document.activeElement !== searchInput) {
            if (state.focusedSectionIndex >= 0) {
                e.preventDefault();
                toggleFocusedSection();
            }
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

    // get export JavaScript for standalone HTML export.
    // MAINTENANCE: this minified JS provides basic filtering/search in exported HTML.
    // it's a simplified version of the main app logic - update if core filtering changes.
    // the export feature creates offline-viewable HTML files that don't require serving.
    function getExportJs() {
        return '(function(){var output=document.getElementById("output");var searchInput=document.getElementById("search");var phaseTabs=document.querySelectorAll(".phase-tab");var mainContainer=document.querySelector(".main-container");var planToggle=document.getElementById("plan-toggle");var expandAllBtn=document.getElementById("expand-all");var collapseAllBtn=document.getElementById("collapse-all");var currentPhase="all";var searchTerm="";function escapeRegex(s){return s.replace(/[.*+?^${}()|[\\]\\\\]/g,"\\\\$&")}function setHighlight(el,text,term){el.textContent="";if(!term){el.textContent=text;return}try{var re=new RegExp("("+escapeRegex(term)+")","gi");var parts=text.split(re);parts.forEach(function(p){if(p.toLowerCase()===term.toLowerCase()){var h=document.createElement("span");h.className="highlight";h.textContent=p;el.appendChild(h)}else if(p){el.appendChild(document.createTextNode(p))}})}catch(e){el.textContent=text}}function applyFilters(){var sections=output.querySelectorAll(".section-header");sections.forEach(function(sec){var ph=sec.dataset.phase;var phMatch=currentPhase==="all"||ph===currentPhase;var hasSearch=!searchTerm;if(searchTerm){sec.querySelectorAll(".output-line").forEach(function(ln){var c=ln.querySelector(".content");var t=c.dataset.originalText||c.textContent;if(t.toLowerCase().includes(searchTerm.toLowerCase()))hasSearch=true})}if(phMatch&&hasSearch){sec.classList.remove("hidden")}else{sec.classList.add("hidden")}});output.querySelectorAll(".output-line").forEach(function(ln){var ph=ln.dataset.phase;var c=ln.querySelector(".content");var t=c.dataset.originalText||c.textContent;var phMatch=currentPhase==="all"||ph===currentPhase;var sMatch=!searchTerm||t.toLowerCase().includes(searchTerm.toLowerCase());if(phMatch&&sMatch){ln.classList.remove("hidden")}else{ln.classList.add("hidden")}setHighlight(c,t,searchTerm)})}phaseTabs.forEach(function(tab){tab.addEventListener("click",function(){currentPhase=tab.dataset.phase;phaseTabs.forEach(function(t){t.classList.toggle("active",t.dataset.phase===currentPhase)});applyFilters()})});searchInput.addEventListener("input",function(){searchTerm=searchInput.value.trim();applyFilters()});planToggle.addEventListener("click",function(){mainContainer.classList.toggle("plan-collapsed");planToggle.textContent=mainContainer.classList.contains("plan-collapsed")?"◀":"▶"});expandAllBtn.addEventListener("click",function(){output.querySelectorAll(".section-header").forEach(function(s){s.open=true})});collapseAllBtn.addEventListener("click",function(){output.querySelectorAll(".section-header").forEach(function(s){s.open=false})});document.addEventListener("keydown",function(e){if(e.key==="/"&&document.activeElement!==searchInput){e.preventDefault();searchInput.focus()}if(e.key==="Escape"){searchInput.value="";searchTerm="";searchInput.blur();applyFilters()}if((e.key==="p"||e.key==="P")&&document.activeElement!==searchInput){e.preventDefault();mainContainer.classList.toggle("plan-collapsed");planToggle.textContent=mainContainer.classList.contains("plan-collapsed")?"◀":"▶"}})})();';
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
    function buildExportHead(safeTitle, css) {
        return '<!DOCTYPE html>\n<html lang="en">\n<head>\n' +
            '<meta charset="UTF-8">\n' +
            '<meta name="viewport" content="width=device-width, initial-scale=1.0">\n' +
            '<title>' + safeTitle + ' - Export</title>\n' +
            '<style>\n' + css + '</style>\n</head>\n';
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
            '</div>\n';
    }

    // build export HTML main content section
    function buildExportMain(clones) {
        return '<div class="main-container">\n' +
            '<main class="output-panel">\n' +
            '<div id="output">\n' + clones.output.innerHTML + '\n</div>\n' +
            '</main>\n' +
            '<aside class="plan-panel">\n' +
            '<div class="plan-panel-header">\n' +
            '<span class="plan-panel-title">Plan</span>\n' +
            '<button class="plan-toggle" id="plan-toggle">▶</button>\n' +
            '</div>\n' +
            '<div class="plan-collapsed-label">Plan</div>\n' +
            '<div class="plan-content">\n' + clones.plan.innerHTML + '\n</div>\n' +
            '</aside>\n' +
            '</div>\n' +
            '<script>\n' + getExportJs() + '\n<\/script>\n' +
            '</body>\n</html>';
    }

    // build export HTML document - uses escapeHtml to prevent XSS from user content
    function buildExportHtml(data, clones, css) {
        var safeTitle = escapeHtml(data.title);
        var safePlanName = escapeHtml(data.planName);
        var safeBranch = escapeHtml(data.branch);
        var safeElapsed = escapeHtml(data.elapsed);
        var safeStatus = escapeHtml(data.status);
        var safeStatusClass = escapeHtml(data.statusClass);

        return buildExportHead(safeTitle, css) +
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
        // fetch current stylesheet instead of using hardcoded copy
        fetch('/static/styles.css')
            .then(function(response) {
                if (!response.ok) throw new Error('Failed to fetch styles');
                return response.text();
            })
            .then(function(css) {
                var data = collectSessionData();
                var clones = cloneContentForExport();
                var html = buildExportHtml(data, clones, css);
                var filename = 'ralphex-' + data.planName.replace(/[^a-z0-9]/gi, '-') + '.html';
                downloadFile(html, filename, 'text/html');
            })
            .catch(function(err) {
                console.error('Export failed:', err);
                alert('Export failed: ' + err.message);
            });
    }

    exportBtn.addEventListener('click', exportSession);

    // expand/collapse all sections (user-initiated, so track preferences)
    function expandAllSections() {
        output.querySelectorAll('.section-header').forEach(function(section) {
            section.open = true;
            var sectionId = section.dataset.sectionId;
            if (sectionId) {
                trackSectionToggle(sectionId, true);
            }
        });
    }

    function collapseAllSections() {
        output.querySelectorAll('.section-header').forEach(function(section) {
            section.open = false;
            var sectionId = section.dataset.sectionId;
            if (sectionId) {
                trackSectionToggle(sectionId, false);
            }
        });
    }

    // run terminal cleanup once (guard prevents double-calls)
    function runTerminalCleanupOnce() {
        if (state.hasRunTerminalCleanup) return;
        state.hasRunTerminalCleanup = true;
        clearActiveTaskStyling();
    }

    // clear active task styling (used when session completes)
    function clearActiveTaskStyling() {
        var activeTasks = planContent.querySelectorAll('.plan-task.active');
        activeTasks.forEach(function(task) {
            task.classList.remove('active');
            var statusEl = task.querySelector('.plan-task-status');
            if (statusEl) {
                statusEl.classList.remove('active');
                // if all checkboxes are checked, mark as done
                var unchecked = task.querySelectorAll('.plan-checkbox:not(.checked)');
                if (unchecked.length === 0) {
                    statusEl.classList.add('done');
                    statusEl.textContent = '✓';
                }
            }
        });
    }

    expandAllBtn.addEventListener('click', expandAllSections);
    collapseAllBtn.addEventListener('click', collapseAllSections);

    // help modal handlers (with null checks for SSR/test environments)
    if (helpBtn) {
        helpBtn.addEventListener('click', showHelp);
    }
    if (helpCloseBtn) {
        helpCloseBtn.addEventListener('click', hideHelp);
    }
    if (helpOverlay) {
        helpOverlay.addEventListener('click', function(e) {
            if (e.target === helpOverlay) {
                hideHelp();
            }
        });
    }






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
