// placeholder app.js - full implementation in Task 3
(function() {
    'use strict';

    const output = document.getElementById('output');
    const status = document.getElementById('status');

    function connect() {
        const source = new EventSource('/events');

        source.onopen = function() {
            status.textContent = 'connected';
            status.style.color = '#0f0';
        };

        source.onmessage = function(e) {
            try {
                const event = JSON.parse(e.data);
                const line = document.createElement('div');
                line.textContent = '[' + event.phase + '] ' + event.text;
                output.appendChild(line);
                output.scrollTop = output.scrollHeight;
            } catch (err) {
                console.error('parse error:', err);
            }
        };

        source.onerror = function() {
            status.textContent = 'disconnected';
            status.style.color = '#f00';
            source.close();
            // reconnect after delay
            setTimeout(connect, 3000);
        };
    }

    connect();
})();
