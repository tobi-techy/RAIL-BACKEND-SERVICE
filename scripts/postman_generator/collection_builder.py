#!/usr/bin/env python3
"""Build Postman collection from extracted endpoints"""
import json
import os
from typing import List, Dict, Any
from endpoint_extractor import Endpoint

class PostmanCollectionBuilder:
    """Build Postman collection JSON"""
    
    def __init__(self):
        self.collection = {
            'info': {
                'name': 'RAIL Service API - Auto-Generated',
                'description': 'Auto-generated comprehensive API collection for RAIL Platform',
                'schema': 'https://schema.getpostman.com/json/collection/v2.1.0/collection.json',
                'version': '2.0.0'
            },
            'auth': {
                'type': 'bearer',
                'bearer': [{'key': 'token', 'value': '{{access_token}}', 'type': 'string'}]
            },
            'variable': [
                {'key': 'base_url', 'value': 'http://localhost:8080', 'type': 'string'},
                {'key': 'access_token', 'value': '', 'type': 'string'},
                {'key': 'refresh_token', 'value': '', 'type': 'string'},
                {'key': 'user_id', 'value': '', 'type': 'string'}
            ],
            'item': []
        }
        
        # Load metadata from JSON file
        metadata_file = os.path.join(os.path.dirname(__file__), 'endpoint_metadata.json')
        try:
            with open(metadata_file, 'r') as f:
                self.metadata = json.load(f)
        except FileNotFoundError:
            print(f"Warning: {metadata_file} not found, using minimal metadata")
            self.metadata = {}
    
    def _create_request(self, endpoint: Endpoint) -> Dict[str, Any]:
        """Create a Postman request item from endpoint"""
        handler_name = endpoint.handler
        metadata = self.metadata.get(handler_name, {})
        
        path = endpoint.path if endpoint.path.startswith('/') else '/' + endpoint.path
        
        request = {
            'name': self._format_name(handler_name or path),
            'request': {
                'method': endpoint.method,
                'header': [],
                'url': {
                    'raw': f'{{{{base_url}}}}{path}',
                    'host': ['{{base_url}}'],
                    'path': path.strip('/').split('/')
                },
                'description': metadata.get('description', f'{endpoint.method} {path}')
            },
            'response': []
        }
        
        if not endpoint.auth_required:
            request['request']['auth'] = {'type': 'noauth'}
        
        if endpoint.method in ['POST', 'PUT', 'PATCH'] and 'body' in metadata:
            request['request']['body'] = {
                'mode': 'raw',
                'raw': json.dumps(metadata['body'], indent=2),
                'options': {'raw': {'language': 'json'}}
            }
            request['request']['header'].append({'key': 'Content-Type', 'value': 'application/json'})
        
        if 'response' in metadata:
            request['response'].append({
                'name': 'Success',
                'status': 'OK',
                'code': 200 if endpoint.method == 'GET' else 201,
                '_postman_previewlanguage': 'json',
                'header': [{'key': 'Content-Type', 'value': 'application/json'}],
                'body': json.dumps(metadata['response'], indent=2)
            })
        
        return request
    
    def _format_name(self, name: str) -> str:
        """Format handler name to readable request name"""
        name = name.replace('Handler', '').replace('handler', '')
        result = []
        for i, char in enumerate(name):
            if char.isupper() and i > 0:
                result.append(' ')
            result.append(char)
        return ''.join(result).strip()
    
    def add_folder(self, name: str, description: str, endpoints: List[Endpoint]):
        """Add a folder with endpoints to collection"""
        items = [self._create_request(ep) for ep in endpoints]
        self.collection['item'].append({'name': name, 'description': description, 'item': items})
    
    def build(self, grouped_endpoints: Dict[str, List[Endpoint]]) -> Dict[str, Any]:
        """Build complete collection from grouped endpoints"""
        icons = {
            'Health': '🏥', 'Authentication': '🔐', 'Onboarding': '🚀', 'Users': '👤',
            'Security': '🔒', 'Wallets': '💰', 'Funding': '💸', 'Investment': '📈',
            'Portfolio': '📊', 'Analytics': '📉', 'Market': '💹', 'Scheduled Investments': '🔄',
            'Rebalancing': '⚖️', 'Copy Trading': '👥', 'Roundups': '🪙', 'AI Chat': '🤖',
            'News': '📰', 'Webhooks': '🔔', 'Admin': '⚙️'
        }
        
        for group_name, endpoints in grouped_endpoints.items():
            icon = icons.get(group_name, '📁')
            self.add_folder(f'{icon} {group_name}', f'{group_name} endpoints', endpoints)
        
        return self.collection
    
    def save(self, filepath: str):
        """Save collection to file"""
        with open(filepath, 'w') as f:
            json.dump(self.collection, f, indent=2)
