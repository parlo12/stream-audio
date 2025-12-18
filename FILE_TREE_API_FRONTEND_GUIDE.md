# File Tree API - Frontend Integration Guide

## Overview

The File Tree API provides a hierarchical view of all audio files stored on the server. This endpoint is designed for admin dashboards to visualize file structure, monitor storage, and verify file deletions.

**Use Cases:**
- Display file explorer in admin dashboard
- Monitor storage usage and file sizes
- Verify files were deleted after cleanup operations
- Debug file organization issues
- Audit storage structure

---

## Endpoint Details

### URL
```
GET /api/admin/files/tree
```

### Authentication
**Required**: Admin JWT token

**Headers:**
```http
Authorization: Bearer {admin_jwt_token}
```

### Response Format

**Success (200 OK):**
```json
{
  "tree": {
    "name": "audio",
    "path": "",
    "isDir": true,
    "children": [
      {
        "name": "book_7_segments",
        "path": "book_7_segments",
        "isDir": true,
        "children": [
          {
            "name": "segment_000.mp3",
            "path": "book_7_segments/segment_000.mp3",
            "isDir": false,
            "size": 245632
          }
        ]
      }
    ]
  },
  "root": "/opt/stream-audio-data/audio"
}
```

**Error Responses:**

```json
// 401 Unauthorized - Missing or invalid token
{
  "error": "Unauthorized"
}

// 403 Forbidden - User is not an admin
{
  "error": "Admin access required"
}

// 404 Not Found - Audio directory doesn't exist
{
  "error": "Audio directory not found"
}

// 500 Internal Server Error
{
  "error": "Failed to build file tree: permission denied"
}
```

---

## Data Structure

### FileTreeNode

```typescript
interface FileTreeNode {
  name: string;           // File/directory name (e.g., "book_42.mp3")
  path: string;           // Relative path from root (e.g., "user_123/book_42.mp3")
  isDir: boolean;         // true for directories, false for files
  size?: number;          // File size in bytes (only present for files)
  children?: FileTreeNode[]; // Array of child nodes (only present for directories)
}
```

### Response

```typescript
interface FileTreeResponse {
  tree: FileTreeNode;     // Root node of the tree
  root: string;           // Absolute path on server (e.g., "/opt/stream-audio-data/audio")
}
```

---

## Frontend Implementation

### 1. React Hook (Recommended)

```typescript
// hooks/useFileTree.ts
import { useState, useEffect } from 'react';

interface FileTreeNode {
  name: string;
  path: string;
  isDir: boolean;
  size?: number;
  children?: FileTreeNode[];
}

interface FileTreeResponse {
  tree: FileTreeNode;
  root: string;
}

export function useFileTree() {
  const [tree, setTree] = useState<FileTreeNode | null>(null);
  const [root, setRoot] = useState<string>('');
  const [loading, setLoading] = useState<boolean>(false);
  const [error, setError] = useState<string | null>(null);

  const fetchFileTree = async () => {
    setLoading(true);
    setError(null);

    try {
      const token = localStorage.getItem('adminToken');
      if (!token) {
        throw new Error('No admin token found');
      }

      const response = await fetch('http://68.183.22.205:8080/api/admin/files/tree', {
        method: 'GET',
        headers: {
          'Authorization': `Bearer ${token}`,
          'Content-Type': 'application/json',
        },
      });

      if (!response.ok) {
        const errorData = await response.json();
        throw new Error(errorData.error || 'Failed to fetch file tree');
      }

      const data: FileTreeResponse = await response.json();
      setTree(data.tree);
      setRoot(data.root);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Unknown error');
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    fetchFileTree();
  }, []);

  return { tree, root, loading, error, refetch: fetchFileTree };
}
```

**Usage:**
```tsx
import { useFileTree } from './hooks/useFileTree';

function FileExplorer() {
  const { tree, root, loading, error, refetch } = useFileTree();

  if (loading) return <div>Loading file tree...</div>;
  if (error) return <div>Error: {error}</div>;
  if (!tree) return <div>No files found</div>;

  return (
    <div>
      <h2>File Explorer: {root}</h2>
      <button onClick={refetch}>Refresh</button>
      <FileTreeView node={tree} />
    </div>
  );
}
```

---

### 2. Vanilla JavaScript

```javascript
// fileTreeService.js
class FileTreeService {
  constructor(baseURL, getToken) {
    this.baseURL = baseURL;
    this.getToken = getToken;
  }

  async fetchFileTree() {
    const token = this.getToken();
    if (!token) {
      throw new Error('Authentication required');
    }

    const response = await fetch(`${this.baseURL}/api/admin/files/tree`, {
      method: 'GET',
      headers: {
        'Authorization': `Bearer ${token}`,
        'Content-Type': 'application/json',
      },
    });

    if (!response.ok) {
      const errorData = await response.json();
      throw new Error(errorData.error || 'Failed to fetch file tree');
    }

    return response.json();
  }
}

// Usage
const fileTreeService = new FileTreeService(
  'http://68.183.22.205:8080',
  () => localStorage.getItem('adminToken')
);

async function loadFileTree() {
  try {
    const { tree, root } = await fileTreeService.fetchFileTree();
    console.log('File tree root:', root);
    renderFileTree(tree);
  } catch (error) {
    console.error('Error loading file tree:', error);
    showError(error.message);
  }
}
```

---

### 3. Axios Implementation

```typescript
import axios from 'axios';

const API_BASE_URL = 'http://68.183.22.205:8080/api';

const apiClient = axios.create({
  baseURL: API_BASE_URL,
  headers: {
    'Content-Type': 'application/json',
  },
});

// Add auth token to all requests
apiClient.interceptors.request.use((config) => {
  const token = localStorage.getItem('adminToken');
  if (token) {
    config.headers.Authorization = `Bearer ${token}`;
  }
  return config;
});

export const fetchFileTree = async (): Promise<FileTreeResponse> => {
  const response = await apiClient.get('/admin/files/tree');
  return response.data;
};

// Usage
import { fetchFileTree } from './api/fileTree';

async function loadFileTree() {
  try {
    const { tree, root } = await fetchFileTree();
    console.log('Loaded file tree from:', root);
    return tree;
  } catch (error) {
    if (axios.isAxiosError(error)) {
      console.error('API Error:', error.response?.data?.error);
    }
    throw error;
  }
}
```

---

## UI Components

### 1. React File Tree Component

```tsx
import React, { useState } from 'react';
import { ChevronRight, ChevronDown, Folder, File } from 'lucide-react';

interface FileTreeNode {
  name: string;
  path: string;
  isDir: boolean;
  size?: number;
  children?: FileTreeNode[];
}

interface FileTreeViewProps {
  node: FileTreeNode;
  level?: number;
}

function formatBytes(bytes: number): string {
  if (bytes === 0) return '0 Bytes';
  const k = 1024;
  const sizes = ['Bytes', 'KB', 'MB', 'GB'];
  const i = Math.floor(Math.log(bytes) / Math.log(k));
  return Math.round(bytes / Math.pow(k, i) * 100) / 100 + ' ' + sizes[i];
}

function FileTreeView({ node, level = 0 }: FileTreeViewProps) {
  const [expanded, setExpanded] = useState(level === 0); // Root is expanded by default

  const handleToggle = () => {
    if (node.isDir) {
      setExpanded(!expanded);
    }
  };

  const paddingLeft = level * 20;

  return (
    <div>
      <div
        style={{ paddingLeft: `${paddingLeft}px` }}
        className={`flex items-center gap-2 py-1 px-2 hover:bg-gray-100 cursor-pointer ${
          node.isDir ? 'font-medium' : ''
        }`}
        onClick={handleToggle}
      >
        {node.isDir && (
          <span className="w-4 h-4">
            {expanded ? <ChevronDown size={16} /> : <ChevronRight size={16} />}
          </span>
        )}
        {!node.isDir && <span className="w-4 h-4"></span>}

        {node.isDir ? (
          <Folder size={16} className="text-blue-500" />
        ) : (
          <File size={16} className="text-gray-500" />
        )}

        <span className="flex-1">{node.name}</span>

        {!node.isDir && node.size && (
          <span className="text-sm text-gray-500">{formatBytes(node.size)}</span>
        )}
      </div>

      {node.isDir && expanded && node.children && (
        <div>
          {node.children.map((child, index) => (
            <FileTreeView key={`${child.path}-${index}`} node={child} level={level + 1} />
          ))}
        </div>
      )}
    </div>
  );
}

export default FileTreeView;
```

---

### 2. HTML/CSS File Tree (Vanilla JS)

```html
<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>File Tree Viewer</title>
  <style>
    .file-tree {
      font-family: 'Courier New', monospace;
      padding: 20px;
      background: #f5f5f5;
      border-radius: 8px;
    }

    .tree-node {
      padding: 4px 8px;
      cursor: pointer;
      display: flex;
      align-items: center;
      gap: 8px;
    }

    .tree-node:hover {
      background: #e0e0e0;
    }

    .tree-node.directory {
      font-weight: bold;
    }

    .tree-children {
      margin-left: 20px;
    }

    .tree-children.collapsed {
      display: none;
    }

    .icon {
      width: 16px;
      height: 16px;
      display: inline-block;
    }

    .icon-folder::before {
      content: '📁';
    }

    .icon-file::before {
      content: '📄';
    }

    .file-size {
      margin-left: auto;
      color: #666;
      font-size: 0.9em;
    }

    .error {
      color: red;
      padding: 10px;
      background: #fee;
      border-radius: 4px;
    }

    .loading {
      text-align: center;
      padding: 20px;
    }
  </style>
</head>
<body>
  <div id="app">
    <h1>Audio File Explorer</h1>
    <button id="refreshBtn">Refresh</button>
    <div id="fileTree"></div>
  </div>

  <script>
    const API_URL = 'http://68.183.22.205:8080/api/admin/files/tree';
    const container = document.getElementById('fileTree');
    const refreshBtn = document.getElementById('refreshBtn');

    function formatBytes(bytes) {
      if (bytes === 0) return '0 Bytes';
      const k = 1024;
      const sizes = ['Bytes', 'KB', 'MB', 'GB'];
      const i = Math.floor(Math.log(bytes) / Math.log(k));
      return (bytes / Math.pow(k, i)).toFixed(2) + ' ' + sizes[i];
    }

    function createTreeNode(node, level = 0) {
      const div = document.createElement('div');

      const nodeDiv = document.createElement('div');
      nodeDiv.className = `tree-node ${node.isDir ? 'directory' : 'file'}`;
      nodeDiv.style.paddingLeft = `${level * 20}px`;

      const icon = document.createElement('span');
      icon.className = node.isDir ? 'icon icon-folder' : 'icon icon-file';
      nodeDiv.appendChild(icon);

      const name = document.createElement('span');
      name.textContent = node.name;
      nodeDiv.appendChild(name);

      if (!node.isDir && node.size) {
        const size = document.createElement('span');
        size.className = 'file-size';
        size.textContent = formatBytes(node.size);
        nodeDiv.appendChild(size);
      }

      div.appendChild(nodeDiv);

      if (node.isDir && node.children) {
        const childrenDiv = document.createElement('div');
        childrenDiv.className = 'tree-children';

        node.children.forEach(child => {
          childrenDiv.appendChild(createTreeNode(child, level + 1));
        });

        div.appendChild(childrenDiv);

        nodeDiv.addEventListener('click', () => {
          childrenDiv.classList.toggle('collapsed');
        });
      }

      return div;
    }

    async function loadFileTree() {
      container.innerHTML = '<div class="loading">Loading...</div>';

      try {
        const token = localStorage.getItem('adminToken');
        if (!token) {
          throw new Error('No admin token found. Please login.');
        }

        const response = await fetch(API_URL, {
          headers: {
            'Authorization': `Bearer ${token}`,
          },
        });

        if (!response.ok) {
          const error = await response.json();
          throw new Error(error.error || 'Failed to load file tree');
        }

        const { tree, root } = await response.json();

        container.innerHTML = '';
        const rootInfo = document.createElement('div');
        rootInfo.style.marginBottom = '10px';
        rootInfo.innerHTML = `<strong>Root:</strong> ${root}`;
        container.appendChild(rootInfo);

        container.appendChild(createTreeNode(tree));
      } catch (error) {
        container.innerHTML = `<div class="error">Error: ${error.message}</div>`;
      }
    }

    refreshBtn.addEventListener('click', loadFileTree);
    loadFileTree();
  </script>
</body>
</html>
```

---

## Advanced Features

### 1. Search Functionality

```typescript
function searchFileTree(node: FileTreeNode, query: string): FileTreeNode[] {
  const results: FileTreeNode[] = [];

  function traverse(n: FileTreeNode) {
    if (n.name.toLowerCase().includes(query.toLowerCase())) {
      results.push(n);
    }

    if (n.children) {
      n.children.forEach(traverse);
    }
  }

  traverse(node);
  return results;
}

// Usage
const results = searchFileTree(tree, 'book_42');
console.log('Found files:', results);
```

---

### 2. Calculate Total Storage

```typescript
function calculateTotalSize(node: FileTreeNode): number {
  if (!node.isDir) {
    return node.size || 0;
  }

  let total = 0;
  if (node.children) {
    for (const child of node.children) {
      total += calculateTotalSize(child);
    }
  }

  return total;
}

// Usage
const totalBytes = calculateTotalSize(tree);
console.log('Total storage:', formatBytes(totalBytes));
```

---

### 3. File Count Statistics

```typescript
interface FileStats {
  totalFiles: number;
  totalDirs: number;
  totalSize: number;
  filesByExtension: Record<string, number>;
}

function getFileStats(node: FileTreeNode): FileStats {
  const stats: FileStats = {
    totalFiles: 0,
    totalDirs: 0,
    totalSize: 0,
    filesByExtension: {},
  };

  function traverse(n: FileTreeNode) {
    if (n.isDir) {
      stats.totalDirs++;
      if (n.children) {
        n.children.forEach(traverse);
      }
    } else {
      stats.totalFiles++;
      stats.totalSize += n.size || 0;

      const ext = n.name.split('.').pop()?.toLowerCase() || 'unknown';
      stats.filesByExtension[ext] = (stats.filesByExtension[ext] || 0) + 1;
    }
  }

  traverse(node);
  return stats;
}

// Usage
const stats = getFileStats(tree);
console.log('Statistics:', stats);
// Output: { totalFiles: 150, totalDirs: 20, totalSize: 524288000, filesByExtension: { mp3: 150 } }
```

---

## Common Use Cases

### 1. Verify Files After Deletion

```typescript
async function verifyDeletion(userId: number) {
  const { tree } = await fetchFileTree();

  // Search for files belonging to deleted user
  const userFiles = searchFileTree(tree, `user_${userId}`);

  if (userFiles.length === 0) {
    console.log('✅ All files deleted successfully');
  } else {
    console.warn('⚠️ Some files still exist:', userFiles);
  }
}
```

---

### 2. Monitor Storage Before System Wipe

```typescript
async function showWipePreview() {
  const { tree } = await fetchFileTree();
  const stats = getFileStats(tree);

  const confirmed = confirm(
    `About to delete:\n` +
    `- ${stats.totalFiles} files\n` +
    `- ${stats.totalDirs} directories\n` +
    `- ${formatBytes(stats.totalSize)} total\n\n` +
    `Are you sure?`
  );

  if (confirmed) {
    await wipeSystem();
    await fetchFileTree(); // Refresh to verify
  }
}
```

---

### 3. Auto-Refresh After Operations

```typescript
function FileTreeWithAutoRefresh() {
  const { tree, refetch } = useFileTree();

  // Refresh after deletion
  const handleDelete = async (userId: number) => {
    await deleteUser(userId);
    await refetch(); // Refresh file tree
  };

  return (
    <div>
      <FileTreeView node={tree} />
      <button onClick={() => handleDelete(42)}>Delete User 42</button>
    </div>
  );
}
```

---

## Security Best Practices

### 1. Token Storage

```typescript
// ✅ Good - Secure token storage
const token = localStorage.getItem('adminToken');

// ❌ Bad - Don't hardcode tokens
const token = 'eyJhbGciOiJIUzI1NiIs...';
```

---

### 2. Error Handling

```typescript
async function safeFileTreeFetch() {
  try {
    const data = await fetchFileTree();
    return data;
  } catch (error) {
    if (error.response?.status === 401) {
      // Redirect to login
      window.location.href = '/admin/login';
    } else if (error.response?.status === 403) {
      alert('You do not have admin permissions');
    } else {
      console.error('Failed to load file tree:', error);
    }
    return null;
  }
}
```

---

### 3. HTTPS in Production

```typescript
// ✅ Production
const API_URL = process.env.NODE_ENV === 'production'
  ? 'https://yourdomain.com/api'
  : 'http://localhost:8080/api';

// ❌ Never use HTTP in production
const API_URL = 'http://68.183.22.205:8080/api';
```

---

## Testing

### Unit Test Example (Jest + React Testing Library)

```typescript
import { render, screen, waitFor } from '@testing-library/react';
import { rest } from 'msw';
import { setupServer } from 'msw/node';
import FileExplorer from './FileExplorer';

const mockFileTree = {
  tree: {
    name: 'audio',
    path: '',
    isDir: true,
    children: [
      { name: 'test.mp3', path: 'test.mp3', isDir: false, size: 1024 }
    ]
  },
  root: '/opt/stream-audio-data/audio'
};

const server = setupServer(
  rest.get('/api/admin/files/tree', (req, res, ctx) => {
    return res(ctx.json(mockFileTree));
  })
);

beforeAll(() => server.listen());
afterEach(() => server.resetHandlers());
afterAll(() => server.close());

test('renders file tree', async () => {
  render(<FileExplorer />);

  await waitFor(() => {
    expect(screen.getByText('test.mp3')).toBeInTheDocument();
  });
});
```

---

## Summary

✅ **Key Points:**
- Requires admin authentication via JWT token
- Returns recursive tree structure with file sizes
- Use for monitoring, debugging, and verification
- Implement refresh functionality for real-time updates
- Add search and statistics for better UX

📚 **Resources:**
- API Documentation: [ADMIN_MAINTENANCE_API.md](ADMIN_MAINTENANCE_API.md)
- Authentication Guide: [ADMIN_AUTH_FIX_SUMMARY.md](ADMIN_AUTH_FIX_SUMMARY.md)

**Last Updated:** December 17, 2025