package webserver

const plaidLinkHTML = `<!DOCTYPE html>
<html>
<head>
    <title>Connect Bank Account — Vero</title>
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <script src="https://cdn.plaid.com/link/v2/stable/link-initialize.js"></script>
    <style>
        body {
            font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
            display: flex;
            align-items: center;
            justify-content: center;
            min-height: 100vh;
            margin: 0;
            background: #f5f5f5;
            color: #333;
        }
        .container {
            text-align: center;
            padding: 2rem;
        }
        .spinner {
            border: 3px solid #e0e0e0;
            border-top: 3px solid #333;
            border-radius: 50%;
            width: 32px;
            height: 32px;
            animation: spin 1s linear infinite;
            margin: 1rem auto;
        }
        @keyframes spin {
            0% { transform: rotate(0deg); }
            100% { transform: rotate(360deg); }
        }
        .error { color: #d32f2f; }
    </style>
</head>
<body>
    <div class="container">
        <div class="spinner" id="spinner"></div>
        <p id="status">Opening Plaid Link...</p>
    </div>
    <script>
        const handler = Plaid.create({
            token: '{{.LinkToken}}',
            onSuccess: function(publicToken, metadata) {
                document.getElementById('spinner').style.display = 'block';
                document.getElementById('status').textContent = 'Connecting your account...';

                fetch('{{.CallbackURL}}', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({
                        public_token: publicToken,
                        link_token: '{{.LinkToken}}'
                    })
                })
                .then(function(resp) { return resp.json(); })
                .then(function() {
                    window.location.href = '/plaid/success';
                })
                .catch(function(err) {
                    document.getElementById('status').innerHTML =
                        '<span class="error">Something went wrong. Please close this tab and try again.</span>';
                    document.getElementById('spinner').style.display = 'none';
                });
            },
            onExit: function(err) {
                if (err) {
                    document.getElementById('status').innerHTML =
                        '<span class="error">Connection cancelled. You can close this tab.</span>';
                } else {
                    document.getElementById('status').textContent = 'You can close this tab.';
                }
                document.getElementById('spinner').style.display = 'none';
            }
        });

        handler.open();
    </script>
</body>
</html>`

const successHTML = `<!DOCTYPE html>
<html>
<head>
    <title>Bank Connected — Vero</title>
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <style>
        body {
            font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
            display: flex;
            align-items: center;
            justify-content: center;
            min-height: 100vh;
            margin: 0;
            background: #f5f5f5;
            color: #333;
        }
        .container { text-align: center; padding: 2rem; }
        .check { font-size: 3rem; margin-bottom: 1rem; }
    </style>
</head>
<body>
    <div class="container">
        <div class="check">&#10003;</div>
        <h2>Bank Account Connected</h2>
        <p>You can close this tab and return to your AI assistant.</p>
    </div>
</body>
</html>`

const receiptUploadHTML = `<!DOCTYPE html>
<html>
<head>
    <title>Upload Receipt — Vero</title>
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <style>
        body {
            font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
            display: flex;
            align-items: center;
            justify-content: center;
            min-height: 100vh;
            margin: 0;
            background: #f5f5f5;
            color: #333;
        }
        .container { text-align: center; padding: 2rem; max-width: 500px; }
        .dropzone {
            border: 2px dashed #ccc;
            border-radius: 12px;
            padding: 3rem 2rem;
            cursor: pointer;
            transition: border-color 0.2s, background 0.2s;
            margin-bottom: 1rem;
        }
        .dropzone:hover, .dropzone.dragover {
            border-color: #333;
            background: #e8e8e8;
        }
        .dropzone p { margin: 0; font-size: 1.1rem; }
        .dropzone .hint { font-size: 0.85rem; color: #888; margin-top: 0.5rem; }
        .spinner {
            border: 3px solid #e0e0e0;
            border-top: 3px solid #333;
            border-radius: 50%;
            width: 32px;
            height: 32px;
            animation: spin 1s linear infinite;
            margin: 1rem auto;
            display: none;
        }
        @keyframes spin {
            0% { transform: rotate(0deg); }
            100% { transform: rotate(360deg); }
        }
        #preview { max-width: 200px; max-height: 200px; margin: 1rem auto; display: none; border-radius: 8px; }
        #statusText { margin-top: 1rem; }
    </style>
    <script src="https://cdn.jsdelivr.net/npm/heic2any@0.0.4/dist/heic2any.min.js"></script>
</head>
<body>
    <div class="container">
        <h2>Upload Receipt</h2>
        <div class="dropzone" id="dropzone">
            <p>Drop receipt image here</p>
            <p class="hint">or click to select a file</p>
        </div>
        <img id="preview" />
        <div class="spinner" id="spinner"></div>
        <p id="statusText"></p>
        <input type="file" id="fileInput" accept="image/*" style="display:none" />
    </div>
    <script>
        var dropzone = document.getElementById('dropzone');
        var fileInput = document.getElementById('fileInput');
        var preview = document.getElementById('preview');
        var spinner = document.getElementById('spinner');

        dropzone.addEventListener('click', function() { fileInput.click(); });
        dropzone.addEventListener('dragover', function(e) {
            e.preventDefault();
            dropzone.classList.add('dragover');
        });
        dropzone.addEventListener('dragleave', function() {
            dropzone.classList.remove('dragover');
        });
        dropzone.addEventListener('drop', function(e) {
            e.preventDefault();
            dropzone.classList.remove('dragover');
            if (e.dataTransfer.files.length > 0) upload(e.dataTransfer.files[0]);
        });
        fileInput.addEventListener('change', function() {
            if (fileInput.files.length > 0) upload(fileInput.files[0]);
        });

        var MAX_WIDTH = 1200;
        var JPEG_QUALITY = 0.6;

        function upload(file) {
            if (file.size > 20 * 1024 * 1024) {
                document.getElementById('statusText').textContent = 'Image is too large (max 20 MB).';
                document.getElementById('statusText').style.color = '#d32f2f';
                return;
            }
            dropzone.style.display = 'none';
            spinner.style.display = 'block';
            document.getElementById('statusText').textContent = 'Compressing and uploading receipt...';
            compressAndUpload(file);
        }

        function compressAndUpload(file) {
            var isHeic = file.type === 'image/heic' || file.type === 'image/heif' ||
                         /\.(heic|heif)$/i.test(file.name);

            if (isHeic && typeof heic2any !== 'undefined') {
                heic2any({ blob: file, toType: 'image/jpeg', quality: JPEG_QUALITY })
                    .then(function(blob) {
                        var converted = new File([blob], file.name.replace(/\.[^.]+$/, '.jpg'), { type: 'image/jpeg' });
                        compressWithCanvas(converted);
                    })
                    .catch(function() { sendFile(file); });
            } else {
                compressWithCanvas(file);
            }
        }

        function compressWithCanvas(file) {
            var img = new Image();
            img.onload = function() {
                var w = img.width, h = img.height;
                if (w > MAX_WIDTH) { h = Math.round(h * MAX_WIDTH / w); w = MAX_WIDTH; }
                var canvas = document.createElement('canvas');
                canvas.width = w; canvas.height = h;
                canvas.getContext('2d').drawImage(img, 0, 0, w, h);
                canvas.toBlob(function(blob) {
                    if (!blob) { sendFile(file); return; }
                    sendFile(new File([blob], file.name.replace(/\.[^.]+$/, '.jpg'), { type: 'image/jpeg' }));
                }, 'image/jpeg', JPEG_QUALITY);
            };
            img.onerror = function() { sendFile(file); };
            img.src = URL.createObjectURL(file);
        }

        function sendFile(file) {
            preview.src = URL.createObjectURL(file);
            preview.style.display = 'block';
            document.getElementById('statusText').textContent = 'Uploading and scanning receipt...';

            var formData = new FormData();
            formData.append('receipt_image', file);
            formData.append('session_id', '{{.SessionID}}');

            var xhr = new XMLHttpRequest();
            xhr.open('POST', '{{.CallbackURL}}');
            xhr.onload = function() {
                spinner.style.display = 'none';
                if (xhr.status === 200) {
                    document.body.innerHTML = '<div style="text-align:center;padding:2rem;font-family:-apple-system,BlinkMacSystemFont,Segoe UI,Roboto,sans-serif"><div style="font-size:3rem;margin-bottom:1rem">&#10003;</div><h2>Receipt Uploaded</h2><p>Your receipt has been processed. You can close this tab and return to your AI assistant for the results.</p></div>';
                } else {
                    document.getElementById('statusText').textContent = 'Upload failed. Please try again.';
                    document.getElementById('statusText').style.color = '#d32f2f';
                    dropzone.style.display = '';
                    preview.style.display = 'none';
                }
            };
            xhr.onerror = function() {
                spinner.style.display = 'none';
                document.getElementById('statusText').textContent = 'Upload failed. Please try again.';
                document.getElementById('statusText').style.color = '#d32f2f';
                dropzone.style.display = '';
                preview.style.display = 'none';
            };
            xhr.send(formData);
        }
    </script>
</body>
</html>`
