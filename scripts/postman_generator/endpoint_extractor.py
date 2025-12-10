#!/usr/bin/env python3
"""
Extract API endpoints from Go route files
"""
import re
import os
from typing import List, Dict, Optional
from dataclasses import dataclass

@dataclass
class Endpoint:
    method: str
    path: str
    handler: str
    auth_required: bool = True
    description: str = ""
    group: str = ""

class EndpointExtractor:
    """Extract endpoints from Go route files"""
    
    def __init__(self, routes_dir: str):
        self.routes_dir = routes_dir
        self.endpoints: List[Endpoint] = []
        
    def extract_from_file(self, filepath: str) -> List[Endpoint]:
        """Extract endpoints from a single Go file"""
        endpoints = []
        
        with open(filepath, 'r') as f:
            content = f.read()
            
        # Extract route definitions
        # Pattern: router.METHOD("/path", handler)
        patterns = [
            r'(\w+)\.GET\("([^"]+)",\s*(\w+)',
            r'(\w+)\.POST\("([^"]+)",\s*(\w+)',
            r'(\w+)\.PUT\("([^"]+)",\s*(\w+)',
            r'(\w+)\.PATCH\("([^"]+)",\s*(\w+)',
            r'(\w+)\.DELETE\("([^"]+)",\s*(\w+)',
        ]
        
        for pattern in patterns:
            matches = re.finditer(pattern, content)
            for match in matches:
                group_var = match.group(1)
                path = match.group(2)
                handler = match.group(3)
                
                # Determine method from pattern
                if 'GET' in pattern:
                    method = 'GET'
                elif 'POST' in pattern:
                    method = 'POST'
                elif 'PUT' in pattern:
                    method = 'PUT'
                elif 'PATCH' in pattern:
                    method = 'PATCH'
                elif 'DELETE' in pattern:
                    method = 'DELETE'
                else:
                    method = 'GET'
                
                # Check if auth is required
                auth_required = 'noauth' not in content.lower() or 'protected' in group_var.lower()
                
                endpoints.append(Endpoint(
                    method=method,
                    path=path,
                    handler=handler,
                    auth_required=auth_required,
                    group=group_var
                ))
        
        return endpoints
    
    def extract_all(self) -> List[Endpoint]:
        """Extract all endpoints from routes directory"""
        endpoints = []
        
        for filename in os.listdir(self.routes_dir):
            if filename.endswith('.go'):
                filepath = os.path.join(self.routes_dir, filename)
                endpoints.extend(self.extract_from_file(filepath))
        
        self.endpoints = endpoints
        return endpoints
    
    def get_grouped_endpoints(self) -> Dict[str, List[Endpoint]]:
        """Group endpoints by category"""
        groups = {
            'Health': [],
            'Authentication': [],
            'Onboarding': [],
            'Users': [],
            'Security': [],
            'Wallets': [],
            'Funding': [],
            'Investment': [],
            'Portfolio': [],
            'Analytics': [],
            'Market': [],
            'Scheduled Investments': [],
            'Rebalancing': [],
            'Copy Trading': [],
            'Roundups': [],
            'AI Chat': [],
            'News': [],
            'Webhooks': [],
            'Admin': []
        }
        
        for endpoint in self.endpoints:
            path = endpoint.path.lower()
            
            if '/health' in path or '/ready' in path or '/live' in path or '/version' in path:
                groups['Health'].append(endpoint)
            elif '/auth' in path:
                groups['Authentication'].append(endpoint)
            elif '/onboarding' in path:
                groups['Onboarding'].append(endpoint)
            elif '/users' in path:
                groups['Users'].append(endpoint)
            elif '/security' in path or '/passcode' in path:
                groups['Security'].append(endpoint)
            elif '/wallet' in path:
                groups['Wallets'].append(endpoint)
            elif '/funding' in path or '/balances' in path:
                groups['Funding'].append(endpoint)
            elif '/investment' in path or '/orders' in path or '/positions' in path:
                groups['Investment'].append(endpoint)
            elif '/portfolio' in path:
                groups['Portfolio'].append(endpoint)
            elif '/analytics' in path:
                groups['Analytics'].append(endpoint)
            elif '/market' in path:
                groups['Market'].append(endpoint)
            elif '/scheduled' in path:
                groups['Scheduled Investments'].append(endpoint)
            elif '/rebalancing' in path:
                groups['Rebalancing'].append(endpoint)
            elif '/copy' in path:
                groups['Copy Trading'].append(endpoint)
            elif '/roundup' in path:
                groups['Roundups'].append(endpoint)
            elif '/ai' in path:
                groups['AI Chat'].append(endpoint)
            elif '/news' in path:
                groups['News'].append(endpoint)
            elif '/webhook' in path:
                groups['Webhooks'].append(endpoint)
            elif '/admin' in path:
                groups['Admin'].append(endpoint)
        
        # Remove empty groups
        return {k: v for k, v in groups.items() if v}
