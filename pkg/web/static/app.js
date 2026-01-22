// ralphex dashboard - SSE streaming and UI handling
(function() {
    'use strict';

    // DOM elements
    const output = document.getElementById('output');
    const status = document.getElementById('status');
    const searchInput = document.getElementById('search');
    const scrollIndicator = document.getElementById('scroll-indicator');
    const scrollToBottomBtn = document.getElementById('scroll-to-bottom');
    const phaseTabs = document.querySelectorAll('.phase-tab');
    const mainContent = document.querySelector('main');

    // state
    let autoScroll = true;
    let currentPhase = 'all';
    let currentSection = null;
    let searchTerm = '';
    let searchTimeout = null;

    // format timestamp for display
    function formatTimestamp(ts) {
        const d = new Date(ts);
        const pad = function(n) { return n.toString().padStart(2, '0'); };
        return pad(d.getFullYear() % 100) + '-' +
               pad(d.getMonth() + 1) + '-' +
               pad(d.getDate()) + ' ' +
               pad(d.getHours()) + ':' +
               pad(d.getMinutes()) + ':' +
               pad(d.getSeconds());
    }

    // escape regex special characters for safe regex creation
    function escapeRegex(str) {
        return str.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
    }

    // set text content with optional search highlighting
    // uses DOM methods to safely construct highlighted content
    function setContentWithHighlight(element, text, term) {
        // clear existing content
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
                    // create highlight span using DOM API
                    const highlight = document.createElement('span');
                    highlight.className = 'highlight';
                    highlight.textContent = part;
                    element.appendChild(highlight);
                } else if (part) {
                    // create text node for non-matching parts
                    element.appendChild(document.createTextNode(part));
                }
            });
        } catch (e) {
            // fallback to plain text on regex error
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
        setContentWithHighlight(content, event.text, searchTerm);

        line.appendChild(timestamp);
        line.appendChild(content);

        // apply phase filter
        if (currentPhase !== 'all' && event.phase !== currentPhase) {
            line.classList.add('hidden');
        }

        // apply search filter
        if (searchTerm && !matchesSearch(event.text, searchTerm)) {
            line.classList.add('hidden');
        }

        return line;
    }

    // create section header (collapsible details element)
    function createSectionHeader(event) {
        const details = document.createElement('details');
        details.className = 'section-header';
        details.dataset.phase = event.phase;
        details.open = true;

        const summary = document.createElement('summary');

        const phaseLabel = document.createElement('span');
        phaseLabel.className = 'section-phase';
        phaseLabel.textContent = event.phase;

        const title = document.createElement('span');
        title.className = 'section-title';
        title.textContent = event.section || event.text;

        summary.appendChild(phaseLabel);
        summary.appendChild(title);

        const content = document.createElement('div');
        content.className = 'section-content';

        details.appendChild(summary);
        details.appendChild(content);

        // apply phase filter
        if (currentPhase !== 'all' && event.phase !== currentPhase) {
            details.classList.add('hidden');
        }

        return details;
    }

    // render event to output
    function renderEvent(event) {
        if (event.type === 'section') {
            // create new collapsible section
            currentSection = createSectionHeader(event);
            output.appendChild(currentSection);
        } else {
            // create output line
            var line = createOutputLine(event);

            // add to current section or root output
            if (currentSection) {
                var content = currentSection.querySelector('.section-content');
                content.appendChild(line);
            } else {
                output.appendChild(line);
            }
        }

        // auto-scroll if enabled
        if (autoScroll) {
            mainContent.scrollTop = mainContent.scrollHeight;
        }
    }

    // update status display
    function setStatus(state, text) {
        status.textContent = text;
        status.className = 'status ' + state;
    }

    // connect to SSE stream
    function connect() {
        setStatus('connecting', 'connecting...');

        var source = new EventSource('/events');

        source.onopen = function() {
            setStatus('connected', 'connected');
        };

        source.onmessage = function(e) {
            try {
                var event = JSON.parse(e.data);
                renderEvent(event);
            } catch (err) {
                console.error('parse error:', err);
            }
        };

        source.onerror = function() {
            setStatus('disconnected', 'disconnected');
            source.close();
            // reconnect after delay
            setTimeout(connect, 3000);
        };
    }

    // phase filter functions
    function setPhaseFilter(phase) {
        currentPhase = phase;

        // update active tab
        phaseTabs.forEach(function(tab) {
            if (tab.dataset.phase === phase) {
                tab.classList.add('active');
            } else {
                tab.classList.remove('active');
            }
        });

        // apply filter to all output lines and sections
        applyFilters();
    }

    // apply all current filters (phase + search)
    function applyFilters() {
        // filter sections
        var sections = output.querySelectorAll('.section-header');
        sections.forEach(function(section) {
            var phase = section.dataset.phase;
            var phaseMatch = currentPhase === 'all' || phase === currentPhase;

            // check if any content in section matches search
            var hasSearchMatch = !searchTerm;
            if (searchTerm) {
                var lines = section.querySelectorAll('.output-line');
                lines.forEach(function(line) {
                    var content = line.querySelector('.content');
                    var originalText = content.dataset.originalText || content.textContent;
                    if (matchesSearch(originalText, searchTerm)) {
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

        // filter individual lines
        var lines = output.querySelectorAll('.output-line');
        lines.forEach(function(line) {
            var phase = line.dataset.phase;
            var content = line.querySelector('.content');
            var originalText = content.dataset.originalText || content.textContent;

            var phaseMatch = currentPhase === 'all' || phase === currentPhase;
            var searchMatch = !searchTerm || matchesSearch(originalText, searchTerm);

            if (phaseMatch && searchMatch) {
                line.classList.remove('hidden');
            } else {
                line.classList.add('hidden');
            }

            // re-apply highlighting using safe DOM methods
            setContentWithHighlight(content, originalText, searchTerm);
        });
    }

    // handle search input with debounce
    function handleSearch() {
        searchTerm = searchInput.value.trim();
        applyFilters();
    }

    // debounced search
    function debouncedSearch() {
        clearTimeout(searchTimeout);
        searchTimeout = setTimeout(handleSearch, 150);
    }

    // scroll tracking
    function checkScroll() {
        var atBottom = mainContent.scrollHeight - mainContent.scrollTop - mainContent.clientHeight < 50;

        if (atBottom) {
            autoScroll = true;
            scrollIndicator.classList.remove('visible');
        } else {
            scrollIndicator.classList.add('visible');
        }
    }

    // manual scroll disables auto-scroll
    function handleManualScroll() {
        var atBottom = mainContent.scrollHeight - mainContent.scrollTop - mainContent.clientHeight < 50;
        if (!atBottom) {
            autoScroll = false;
        }
    }

    // scroll to bottom and re-enable auto-scroll
    function scrollToBottom() {
        mainContent.scrollTop = mainContent.scrollHeight;
        autoScroll = true;
        scrollIndicator.classList.remove('visible');
    }

    // event listeners
    phaseTabs.forEach(function(tab) {
        tab.addEventListener('click', function() {
            setPhaseFilter(tab.dataset.phase);
        });
    });

    searchInput.addEventListener('input', debouncedSearch);

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
    });

    // scroll tracking
    mainContent.addEventListener('scroll', function() {
        checkScroll();
        handleManualScroll();
    });

    scrollToBottomBtn.addEventListener('click', scrollToBottom);

    // start connection
    connect();
})();
