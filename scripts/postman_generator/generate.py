#!/usr/bin/env python3
"""
Main script to generate Postman collection from codebase
Usage: python generate.py [output_file]
"""
import sys
import os

# Add parent directory to path
sys.path.insert(0, os.path.dirname(__file__))

from endpoint_extractor import EndpointExtractor
from collection_builder import PostmanCollectionBuilder

def main():
    # Get paths
    script_dir = os.path.dirname(os.path.abspath(__file__))
    project_root = os.path.dirname(os.path.dirname(script_dir))
    routes_dir = os.path.join(project_root, 'internal', 'api', 'routes')
    
    # Default output file
    output_file = sys.argv[1] if len(sys.argv) > 1 else os.path.join(project_root, 'postman_collection_generated.json')
    
    print(f"🔍 Extracting endpoints from: {routes_dir}")
    
    # Extract endpoints
    extractor = EndpointExtractor(routes_dir)
    endpoints = extractor.extract_all()
    
    print(f"✅ Found {len(endpoints)} endpoints")
    
    # Group endpoints
    grouped = extractor.get_grouped_endpoints()
    
    print(f"📁 Grouped into {len(grouped)} categories")
    for group, eps in grouped.items():
        print(f"   - {group}: {len(eps)} endpoints")
    
    # Build collection
    print(f"\n🔨 Building Postman collection...")
    builder = PostmanCollectionBuilder()
    collection = builder.build(grouped)
    
    # Save collection
    builder.save(output_file)
    
    print(f"✅ Collection saved to: {output_file}")
    print(f"📊 Total folders: {len(collection['item'])}")
    
    total_requests = sum(len(folder['item']) for folder in collection['item'])
    print(f"📊 Total requests: {total_requests}")
    
    print("\n🎉 Done! Import the collection into Postman to start testing.")

if __name__ == '__main__':
    main()
